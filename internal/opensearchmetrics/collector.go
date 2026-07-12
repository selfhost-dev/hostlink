// Package opensearchmetrics collects metrics from an OpenSearch cluster via its REST API.
package opensearchmetrics

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

// Collector collects OpenSearch cluster metrics from a single node endpoint.
// All nodes are queried from that one endpoint — the cluster aggregates internally.
type Collector interface {
	Collect(credential.Credential) (metrics.OpenSearchDatabaseMetrics, error)
}

type collector struct {
	client          *http.Client
	lastIndexTotal  *int64
	lastSearchTotal *int64
	lastTime        time.Time
}

// New returns a Collector that skips TLS verification. OpenSearch ships with
// self-signed demo certificates, so certificate verification is intentionally
// disabled — the traffic is still encrypted.
func New() Collector {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}
	return &collector{
		client: &http.Client{Timeout: 10 * time.Second, Transport: transport},
	}
}

func (c *collector) Collect(cred credential.Credential) (metrics.OpenSearchDatabaseMetrics, error) {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}

	port := cred.Port
	if port == 0 {
		port = 9200
	}

	baseURL := fmt.Sprintf("https://%s:%d", cred.Host, port)

	// ── 1. Cluster health — primary signal ────────────────────────────────
	health, err := c.clusterHealth(baseURL, cred.Username, password)
	if err != nil {
		return metrics.OpenSearchDatabaseMetrics{}, fmt.Errorf("cluster health: %w", err)
	}

	var m metrics.OpenSearchDatabaseMetrics
	m.ClusterStatus = encodeStatus(stringVal(health["status"]))
	m.ActiveShards = intVal(health["active_shards"])
	m.RelocatingShards = intVal(health["relocating_shards"])
	m.InitializingShards = intVal(health["initializing_shards"])
	m.UnassignedShards = intVal(health["unassigned_shards"])
	m.ActivePrimaryShards = intVal(health["active_primary_shards"])
	m.NumberOfNodes = intVal(health["number_of_nodes"])

	// ── 2. Node stats — best-effort; partial failure is tolerated ─────────
	nodes, err := c.nodeStats(baseURL, cred.Username, password)
	if err != nil {
		// Return what we collected from cluster health — enough for basic alerting.
		return m, nil
	}

	var (
		totalHeap      float64
		totalCPU       float64
		totalDiskUsed  float64
		totalDiskTotal float64
		totalIndexing  int64
		totalSearch    int64
		count          int
	)

	for _, raw := range nodes {
		n, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		count++

		if jvm, ok := n["jvm"].(map[string]any); ok {
			if mem, ok := jvm["mem"].(map[string]any); ok {
				totalHeap += floatVal(mem["heap_used_percent"])
			}
		}

		if osMap, ok := n["os"].(map[string]any); ok {
			if cpu, ok := osMap["cpu"].(map[string]any); ok {
				totalCPU += floatVal(cpu["percent"])
			}
		}

		if fs, ok := n["fs"].(map[string]any); ok {
			if total, ok := fs["total"].(map[string]any); ok {
				tb := floatVal(total["total_in_bytes"])
				ab := floatVal(total["available_in_bytes"])
				totalDiskTotal += tb
				totalDiskUsed += tb - ab
			}
		}

		if indices, ok := n["indices"].(map[string]any); ok {
			if indexing, ok := indices["indexing"].(map[string]any); ok {
				totalIndexing += int64(floatVal(indexing["index_total"]))
			}
			if search, ok := indices["search"].(map[string]any); ok {
				totalSearch += int64(floatVal(search["query_total"]))
			}
		}
	}

	if count > 0 {
		m.JvmHeapUsedPercent = totalHeap / float64(count)
		m.CPUPercent = totalCPU / float64(count)
		if totalDiskTotal > 0 {
			m.DiskUsedPercent = totalDiskUsed / totalDiskTotal * 100
		}
	}

	// ── 3. Delta rates — zero on first cycle ──────────────────────────────
	now := time.Now()
	if c.lastIndexTotal != nil {
		elapsed := now.Sub(c.lastTime).Seconds()
		if elapsed > 0 {
			m.IndexingRate = float64(totalIndexing-*c.lastIndexTotal) / elapsed
			m.SearchRate = float64(totalSearch-*c.lastSearchTotal) / elapsed
		}
	}
	c.lastIndexTotal = &totalIndexing
	c.lastSearchTotal = &totalSearch
	c.lastTime = now

	// ── 4. Replication lag proxy ──────────────────────────────────────────
	// OpenSearch has no WAL lag. A value of 0 means the cluster is green with
	// no shards in motion — safe to consider fully in sync. 99 signals that
	// shards are still relocating or the cluster is not yet green.
	if m.ClusterStatus == 0 && m.RelocatingShards == 0 && m.InitializingShards == 0 {
		m.ReplicationLagSeconds = 0
	} else {
		m.ReplicationLagSeconds = 99
	}

	return m, nil
}

func (c *collector) clusterHealth(baseURL, user, password string) (map[string]any, error) {
	body, err := c.get(baseURL+"/_cluster/health", user, password)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse cluster health: %w", err)
	}
	return result, nil
}

func (c *collector) nodeStats(baseURL, user, password string) ([]any, error) {
	body, err := c.get(baseURL+"/_nodes/stats/jvm,os,fs,indices", user, password)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse node stats: %w", err)
	}
	nodes, ok := result["nodes"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("missing nodes in response")
	}
	out := make([]any, 0, len(nodes))
	for _, v := range nodes {
		out = append(out, v)
	}
	return out, nil
}

func (c *collector) get(url, user, password string) ([]byte, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(user, password)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("opensearch http %d: %s", resp.StatusCode, snippet)
	}
	return io.ReadAll(resp.Body)
}

// encodeStatus converts the OpenSearch cluster status string to an integer so
// that numeric alert rules can compare against it (0=green, 1=yellow, 2=red).
func encodeStatus(s string) int {
	switch s {
	case "green":
		return 0
	case "yellow":
		return 1
	default:
		return 2
	}
}

func floatVal(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int64:
		return float64(x)
	case int:
		return float64(x)
	}
	return 0
}

func intVal(v any) int {
	return int(floatVal(v))
}

func stringVal(v any) string {
	s, _ := v.(string)
	return s
}
