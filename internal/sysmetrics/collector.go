package sysmetrics

import (
	"context"
	"fmt"
	"time"

	"hostlink/domain/metrics"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
)

type CPUStats struct {
	User    float64
	System  float64
	Idle    float64
	Nice    float64
	Iowait  float64
	Irq     float64
	Softirq float64
	Steal   float64
}

func (c CPUStats) Total() float64 {
	return c.User + c.System + c.Idle + c.Nice + c.Iowait + c.Irq + c.Softirq + c.Steal
}

type MemoryStats struct {
	UsedPercent float64
}

type SwapStats struct {
	UsedPercent float64
}

type LoadStats struct {
	Load1  float64
	Load5  float64
	Load15 float64
}

type DiskStats struct {
	UsedPercent float64
}

type SystemCollector interface {
	CPUTimes(ctx context.Context) (CPUStats, error)
	VirtualMemory(ctx context.Context) (MemoryStats, error)
	SwapMemory(ctx context.Context) (SwapStats, error)
	LoadAvg(ctx context.Context) (LoadStats, error)
	DiskUsage(ctx context.Context) (DiskStats, error)
}

type Collector interface {
	Collect(ctx context.Context) (metrics.SystemMetrics, error)
}

type Config struct {
	Collector SystemCollector
}

type collector struct {
	sys         SystemCollector
	lastCPU     *CPUStats
	lastCPUTime time.Time
}

func New() Collector {
	return NewWithConfig(nil)
}

func NewWithConfig(cfg *Config) Collector {
	var sys SystemCollector
	if cfg != nil && cfg.Collector != nil {
		sys = cfg.Collector
	} else {
		sys = &gopsutilCollector{}
	}
	return &collector{
		sys: sys,
	}
}

func (c *collector) Collect(ctx context.Context) (metrics.SystemMetrics, error) {
	var m metrics.SystemMetrics

	cpuPercent, cpuIOWait, err := c.collectCPU(ctx)
	if err == nil {
		m.CPUPercent = cpuPercent
		m.CPUIOWaitPercent = cpuIOWait
	}

	memPercent, err := c.collectMemory(ctx)
	if err == nil {
		m.MemoryPercent = memPercent
	}

	l1, l5, l15, err := c.collectLoadAvg(ctx)
	if err == nil {
		m.LoadAvg1 = l1
		m.LoadAvg5 = l5
		m.LoadAvg15 = l15
	}

	swapPercent, err := c.collectSwap(ctx)
	if err == nil {
		m.SwapUsagePercent = swapPercent
	}

	diskPercent, err := c.collectDisk(ctx)
	if err == nil {
		m.DiskUsagePercent = diskPercent
	}

	return m, nil
}

func (c *collector) collectCPU(ctx context.Context) (float64, float64, error) {
	current, err := c.sys.CPUTimes(ctx)
	if err != nil {
		return 0, 0, err
	}

	if c.lastCPU == nil {
		c.lastCPU = &current
		c.lastCPUTime = time.Now()
		return 0, 0, nil
	}

	delta := CPUStats{
		User:    current.User - c.lastCPU.User,
		System:  current.System - c.lastCPU.System,
		Idle:    current.Idle - c.lastCPU.Idle,
		Nice:    current.Nice - c.lastCPU.Nice,
		Iowait:  current.Iowait - c.lastCPU.Iowait,
		Irq:     current.Irq - c.lastCPU.Irq,
		Softirq: current.Softirq - c.lastCPU.Softirq,
		Steal:   current.Steal - c.lastCPU.Steal,
	}

	total := delta.Total()
	if total == 0 {
		return 0, 0, nil
	}

	cpuPercent := (1 - delta.Idle/total) * 100
	ioWaitPercent := (delta.Iowait / total) * 100

	c.lastCPU = &current
	c.lastCPUTime = time.Now()

	return cpuPercent, ioWaitPercent, nil
}

func (c *collector) collectMemory(ctx context.Context) (float64, error) {
	memStats, err := c.sys.VirtualMemory(ctx)
	if err != nil {
		return 0, err
	}
	return memStats.UsedPercent, nil
}

func (c *collector) collectLoadAvg(ctx context.Context) (float64, float64, float64, error) {
	loadStats, err := c.sys.LoadAvg(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	return loadStats.Load1, loadStats.Load5, loadStats.Load15, nil
}

func (c *collector) collectSwap(ctx context.Context) (float64, error) {
	swapStats, err := c.sys.SwapMemory(ctx)
	if err != nil {
		return 0, err
	}
	return swapStats.UsedPercent, nil
}

func (c *collector) collectDisk(ctx context.Context) (float64, error) {
	diskStats, err := c.sys.DiskUsage(ctx)
	if err != nil {
		return 0, err
	}
	return diskStats.UsedPercent, nil
}

type gopsutilCollector struct{}

func (g *gopsutilCollector) CPUTimes(ctx context.Context) (CPUStats, error) {
	times, err := cpu.TimesWithContext(ctx, false)
	if err != nil {
		return CPUStats{}, err
	}
	if len(times) == 0 {
		return CPUStats{}, fmt.Errorf("no cpu times returned")
	}
	t := times[0]
	return CPUStats{
		User:    t.User,
		System:  t.System,
		Idle:    t.Idle,
		Nice:    t.Nice,
		Iowait:  t.Iowait,
		Irq:     t.Irq,
		Softirq: t.Softirq,
		Steal:   t.Steal,
	}, nil
}

func (g *gopsutilCollector) VirtualMemory(ctx context.Context) (MemoryStats, error) {
	m, err := mem.VirtualMemoryWithContext(ctx)
	if err != nil {
		return MemoryStats{}, err
	}
	return MemoryStats{UsedPercent: m.UsedPercent}, nil
}

func (g *gopsutilCollector) SwapMemory(ctx context.Context) (SwapStats, error) {
	s, err := mem.SwapMemoryWithContext(ctx)
	if err != nil {
		return SwapStats{}, err
	}
	return SwapStats{UsedPercent: s.UsedPercent}, nil
}

func (g *gopsutilCollector) LoadAvg(ctx context.Context) (LoadStats, error) {
	l, err := load.AvgWithContext(ctx)
	if err != nil {
		return LoadStats{}, err
	}
	return LoadStats{
		Load1:  l.Load1,
		Load5:  l.Load5,
		Load15: l.Load15,
	}, nil
}

func (g *gopsutilCollector) DiskUsage(ctx context.Context) (DiskStats, error) {
	partitions, err := disk.PartitionsWithContext(ctx, false)
	if err != nil {
		return DiskStats{}, err
	}

	var totalUsed, totalFree uint64
	for _, p := range partitions {
		usage, err := disk.UsageWithContext(ctx, p.Mountpoint)
		if err != nil {
			continue
		}
		totalUsed += usage.Used
		totalFree += usage.Free
	}

	total := totalUsed + totalFree
	if total == 0 {
		return DiskStats{UsedPercent: 0}, nil
	}

	return DiskStats{
		UsedPercent: float64(totalUsed) / float64(total) * 100,
	}, nil
}
