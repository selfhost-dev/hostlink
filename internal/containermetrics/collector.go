// Package containermetrics collects per-container resource and health metrics
// using the Docker API. Coolify-managed containers are identified via labels
// (coolify.managed=true) and reported with project/app metadata as attributes.
// Non-Coolify containers on the host are also collected as a fallback.
package containermetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	domainmetrics "hostlink/domain/metrics"
)

type Collector interface {
	Collect(ctx context.Context) ([]ContainerMetricSet, error)
}

type ContainerMetricSet struct {
	Attributes domainmetrics.ContainerAttributes
	Metrics    domainmetrics.ContainerMetrics
}

// lastStats tracks cumulative network/block I/O counters between collections
// so we can compute per-second rates via delta.
type lastStats struct {
	rxBytes    uint64
	txBytes    uint64
	readBytes  uint64
	writeBytes uint64
	collectedAt time.Time
}

// dockerStatsJSON is a minimal decode of the Docker stats API response.
// Defined inline to avoid importing docker/api/types which has many transitive deps.
type dockerStatsJSON struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage    uint64   `json:"total_usage"`
			PercpuUsage   []uint64 `json:"percpu_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     int    `json:"online_cpus"`
	} `json:"cpu_stats"`
	PreCPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64            `json:"usage"`
		Limit uint64            `json:"limit"`
		Stats map[string]uint64 `json:"stats"`
	} `json:"memory_stats"`
	Networks map[string]struct {
		RxBytes uint64 `json:"rx_bytes"`
		TxBytes uint64 `json:"tx_bytes"`
	} `json:"networks"`
	BlkioStats struct {
		IoServiceBytesRecursive []struct {
			Op    string `json:"op"`
			Value uint64 `json:"value"`
		} `json:"io_service_bytes_recursive"`
	} `json:"blkio_stats"`
	PidsStats struct {
		Current uint64 `json:"current"`
	} `json:"pids_stats"`
}

type containerCollector struct {
	client    *dockerclient.Client
	mu        sync.Mutex
	lastStats map[string]lastStats // keyed by container ID
}

func New() Collector {
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		// Docker unavailable — return a collector that reports nothing
		return &containerCollector{lastStats: make(map[string]lastStats)}
	}
	return &containerCollector{
		client:    cli,
		lastStats: make(map[string]lastStats),
	}
}

func (cc *containerCollector) Collect(ctx context.Context) ([]ContainerMetricSet, error) {
	if cc.client == nil {
		return nil, nil
	}

	containers, err := cc.listCoolifyContainers(ctx)
	if err != nil {
		return nil, fmt.Errorf("list containers: %w", err)
	}

	var results []ContainerMetricSet
	for _, c := range containers {
		ms, err := cc.collectOne(ctx, c.ID, c.Names, c.Image, c.Labels)
		if err != nil {
			// Non-fatal: one bad container shouldn't stop the rest
			continue
		}
		results = append(results, ms)
	}
	return results, nil
}

// listCoolifyContainers returns Coolify-managed running containers.
// Falls back to all running containers if no Coolify containers are found.
func (cc *containerCollector) listCoolifyContainers(ctx context.Context) ([]container.Summary, error) {
	f := filters.NewArgs()
	f.Add("label", "coolify.managed=true")
	f.Add("status", "running")

	coolify, err := cc.client.ContainerList(ctx, container.ListOptions{Filters: f})
	if err != nil {
		return nil, err
	}
	if len(coolify) > 0 {
		return coolify, nil
	}

	// Fallback: all running containers (host without Coolify)
	all, err := cc.client.ContainerList(ctx, container.ListOptions{
		Filters: filters.NewArgs(filters.Arg("status", "running")),
	})
	return all, err
}

func (cc *containerCollector) collectOne(
	ctx context.Context,
	id string,
	names []string,
	image string,
	labels map[string]string,
) (ContainerMetricSet, error) {
	// Stats (CPU, memory, network, block I/O)
	statsReader, err := cc.client.ContainerStats(ctx, id, false)
	if err != nil {
		return ContainerMetricSet{}, fmt.Errorf("stats %s: %w", id[:12], err)
	}
	defer statsReader.Body.Close()

	var raw dockerStatsJSON
	if err := json.NewDecoder(statsReader.Body).Decode(&raw); err != nil {
		return ContainerMetricSet{}, fmt.Errorf("decode stats %s: %w", id[:12], err)
	}

	// Inspect (restart count, exit code, status, uptime)
	inspect, err := cc.client.ContainerInspect(ctx, id)
	if err != nil {
		return ContainerMetricSet{}, fmt.Errorf("inspect %s: %w", id[:12], err)
	}

	m := domainmetrics.ContainerMetrics{
		Up:           inspect.State.Running,
		PIDs:         int(raw.PidsStats.Current),
		RestartCount: inspect.RestartCount,
		ExitCode:     inspect.State.ExitCode,
		Status:       inspect.State.Status,
	}

	// CPU %
	cpuDelta := float64(raw.CPUStats.CPUUsage.TotalUsage - raw.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(raw.CPUStats.SystemCPUUsage - raw.PreCPUStats.SystemCPUUsage)
	numCPUs := raw.CPUStats.OnlineCPUs
	if numCPUs == 0 {
		numCPUs = len(raw.CPUStats.CPUUsage.PercpuUsage)
	}
	if sysDelta > 0 && cpuDelta > 0 && numCPUs > 0 {
		m.CPUPercent = (cpuDelta / sysDelta) * float64(numCPUs) * 100.0
	}

	// Memory — subtract page cache from usage for a realistic working-set number
	memUsed := raw.MemoryStats.Usage
	if cache, ok := raw.MemoryStats.Stats["cache"]; ok && memUsed > cache {
		memUsed -= cache
	}
	m.MemoryUsageBytes = int64(memUsed)
	m.MemoryLimitBytes = int64(raw.MemoryStats.Limit)
	if raw.MemoryStats.Limit > 0 {
		m.MemoryPercent = float64(memUsed) / float64(raw.MemoryStats.Limit) * 100.0
	}

	// Network I/O (cumulative → delta → per-sec)
	var rxTotal, txTotal uint64
	for _, net := range raw.Networks {
		rxTotal += net.RxBytes
		txTotal += net.TxBytes
	}

	// Block I/O (cumulative → delta → per-sec)
	var blkRead, blkWrite uint64
	for _, entry := range raw.BlkioStats.IoServiceBytesRecursive {
		switch strings.ToLower(entry.Op) {
		case "read":
			blkRead += entry.Value
		case "write":
			blkWrite += entry.Value
		}
	}

	cc.mu.Lock()
	prev, hasPrev := cc.lastStats[id]
	now := time.Now()
	cc.lastStats[id] = lastStats{
		rxBytes:     rxTotal,
		txBytes:     txTotal,
		readBytes:   blkRead,
		writeBytes:  blkWrite,
		collectedAt: now,
	}
	cc.mu.Unlock()

	if hasPrev {
		elapsed := now.Sub(prev.collectedAt).Seconds()
		if elapsed > 0 {
			m.NetRxBytesPerSec = float64(rxTotal-prev.rxBytes) / elapsed
			m.NetTxBytesPerSec = float64(txTotal-prev.txBytes) / elapsed
			m.BlockReadBytesPerSec = float64(blkRead-prev.readBytes) / elapsed
			m.BlockWriteBytesPerSec = float64(blkWrite-prev.writeBytes) / elapsed
		}
	}

	// Uptime
	if inspect.State.Running && inspect.State.StartedAt != "" {
		if startedAt, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil {
			m.UptimeSeconds = int64(time.Since(startedAt).Seconds())
		}
	}

	// Container name — Docker prefixes names with '/'
	name := id[:12]
	if len(names) > 0 {
		name = strings.TrimPrefix(names[0], "/")
	}

	attrs := domainmetrics.ContainerAttributes{
		ContainerID:          id[:12],
		ContainerName:        name,
		Image:                image,
		CoolifyAppID:         labels["coolify.applicationId"],
		CoolifyProjectID:     labels["coolify.projectId"],
		CoolifyEnvironmentID: labels["coolify.environmentId"],
		CoolifyType:          labels["coolify.type"],
		CoolifyName:          labels["coolify.name"],
	}

	return ContainerMetricSet{Attributes: attrs, Metrics: m}, nil
}
