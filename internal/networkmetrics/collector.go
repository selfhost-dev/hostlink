package networkmetrics

import (
	"context"
	"fmt"
	"time"

	"hostlink/domain/metrics"

	"github.com/shirou/gopsutil/v4/net"
)

type NetworkStats struct {
	RecvBytes uint64
	SentBytes uint64
}

type NetworkCollector interface {
	NetworkIO(ctx context.Context) (NetworkStats, error)
}

type Collector interface {
	Collect(ctx context.Context) (metrics.NetworkMetrics, error)
}

type Config struct {
	Collector NetworkCollector
}

type collector struct {
	sys         NetworkCollector
	lastNetwork *NetworkStats
	lastTime    time.Time
}

func New() Collector {
	return NewWithConfig(nil)
}

func NewWithConfig(cfg *Config) Collector {
	var sys NetworkCollector
	if cfg != nil && cfg.Collector != nil {
		sys = cfg.Collector
	} else {
		sys = &gopsutilCollector{}
	}
	return &collector{
		sys: sys,
	}
}

func (c *collector) Collect(ctx context.Context) (metrics.NetworkMetrics, error) {
	var m metrics.NetworkMetrics

	current, err := c.sys.NetworkIO(ctx)
	if err != nil {
		return m, nil
	}

	if c.lastNetwork == nil {
		c.lastNetwork = &current
		c.lastTime = time.Now()
		return m, nil
	}

	elapsed := time.Since(c.lastTime).Seconds()
	if elapsed == 0 {
		return m, nil
	}

	m.RecvBytesPerSec = float64(current.RecvBytes-c.lastNetwork.RecvBytes) / elapsed
	m.SentBytesPerSec = float64(current.SentBytes-c.lastNetwork.SentBytes) / elapsed

	c.lastNetwork = &current
	c.lastTime = time.Now()

	return m, nil
}

type gopsutilCollector struct{}

func (g *gopsutilCollector) NetworkIO(ctx context.Context) (NetworkStats, error) {
	counters, err := net.IOCountersWithContext(ctx, false)
	if err != nil {
		return NetworkStats{}, err
	}
	if len(counters) == 0 {
		return NetworkStats{}, fmt.Errorf("no network counters returned")
	}
	return NetworkStats{
		RecvBytes: counters[0].BytesRecv,
		SentBytes: counters[0].BytesSent,
	}, nil
}
