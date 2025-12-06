package sysmetrics

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockSystemCollector struct {
	cpuStats  CPUStats
	cpuErr    error
	memStats  MemoryStats
	memErr    error
	swapStats SwapStats
	swapErr   error
	loadStats LoadStats
	loadErr   error
	diskStats DiskStats
	diskErr   error
}

func (m *mockSystemCollector) CPUTimes(ctx context.Context) (CPUStats, error) {
	return m.cpuStats, m.cpuErr
}

func (m *mockSystemCollector) VirtualMemory(ctx context.Context) (MemoryStats, error) {
	return m.memStats, m.memErr
}

func (m *mockSystemCollector) SwapMemory(ctx context.Context) (SwapStats, error) {
	return m.swapStats, m.swapErr
}

func (m *mockSystemCollector) LoadAvg(ctx context.Context) (LoadStats, error) {
	return m.loadStats, m.loadErr
}

func (m *mockSystemCollector) DiskUsage(ctx context.Context) (DiskStats, error) {
	return m.diskStats, m.diskErr
}

// TestCollect_FirstCollection_ReturnsZeroCPU - first collection stores baseline, returns 0 for CPU
func TestCollect_FirstCollection_ReturnsZeroCPU(t *testing.T) {
	mock := &mockSystemCollector{
		cpuStats:  CPUStats{User: 100, System: 50, Idle: 850, Iowait: 10},
		memStats:  MemoryStats{UsedPercent: 45.0},
		swapStats: SwapStats{UsedPercent: 10.0},
		loadStats: LoadStats{Load1: 1.0, Load5: 0.8, Load15: 0.5},
		diskStats: DiskStats{UsedPercent: 60.0},
	}

	c := NewWithConfig(&Config{Collector: mock})
	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics.CPUPercent)
	assert.Equal(t, 0.0, metrics.CPUIOWaitPercent)
	assert.Equal(t, 45.0, metrics.MemoryPercent)
	assert.Equal(t, 10.0, metrics.SwapUsagePercent)
	assert.Equal(t, 1.0, metrics.LoadAvg1)
	assert.Equal(t, 0.8, metrics.LoadAvg5)
	assert.Equal(t, 0.5, metrics.LoadAvg15)
	assert.Equal(t, 60.0, metrics.DiskUsagePercent)
}

// TestCollect_SecondCollection_ReturnsDeltaCPU - second collection calculates delta
func TestCollect_SecondCollection_ReturnsDeltaCPU(t *testing.T) {
	mock := &mockSystemCollector{
		memStats:  MemoryStats{UsedPercent: 45.0},
		swapStats: SwapStats{UsedPercent: 10.0},
		loadStats: LoadStats{Load1: 1.0, Load5: 0.8, Load15: 0.5},
		diskStats: DiskStats{UsedPercent: 60.0},
	}

	c := NewWithConfig(&Config{Collector: mock})

	// First collection - baseline
	mock.cpuStats = CPUStats{User: 100, System: 50, Idle: 840, Iowait: 10}
	c.Collect(context.Background())

	// Second collection - delta
	mock.cpuStats = CPUStats{User: 120, System: 60, Idle: 900, Iowait: 20}
	// Delta: User=20, System=10, Idle=60, Iowait=10, Total=100
	// CPUPercent = (1 - 60/100) * 100 = 40%
	// IOWaitPercent = (10/100) * 100 = 10%

	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 40.0, metrics.CPUPercent)
	assert.Equal(t, 10.0, metrics.CPUIOWaitPercent)
}

// TestCollect_ReturnsAllMetrics - all metrics collected successfully (after baseline)
func TestCollect_ReturnsAllMetrics(t *testing.T) {
	mock := &mockSystemCollector{
		memStats:  MemoryStats{UsedPercent: 45.0},
		swapStats: SwapStats{UsedPercent: 10.0},
		loadStats: LoadStats{Load1: 1.25, Load5: 0.80, Load15: 0.50},
		diskStats: DiskStats{UsedPercent: 60.0},
	}

	c := NewWithConfig(&Config{Collector: mock})

	// First collection - baseline
	mock.cpuStats = CPUStats{User: 100, System: 50, Idle: 840, Iowait: 10}
	c.Collect(context.Background())

	// Second collection
	mock.cpuStats = CPUStats{User: 120, System: 60, Idle: 900, Iowait: 20}

	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 40.0, metrics.CPUPercent)
	assert.Equal(t, 10.0, metrics.CPUIOWaitPercent)
	assert.Equal(t, 45.0, metrics.MemoryPercent)
	assert.Equal(t, 1.25, metrics.LoadAvg1)
	assert.Equal(t, 0.80, metrics.LoadAvg5)
	assert.Equal(t, 0.50, metrics.LoadAvg15)
	assert.Equal(t, 10.0, metrics.SwapUsagePercent)
	assert.Equal(t, 60.0, metrics.DiskUsagePercent)
}

// TestCollect_CPUFailure_ReturnsZero - CPU fails, returns 0 for CPU fields
func TestCollect_CPUFailure_ReturnsZero(t *testing.T) {
	mock := &mockSystemCollector{
		cpuErr:    errors.New("cpu error"),
		memStats:  MemoryStats{UsedPercent: 45.0},
		swapStats: SwapStats{UsedPercent: 10.0},
		loadStats: LoadStats{Load1: 1.0, Load5: 0.8, Load15: 0.5},
		diskStats: DiskStats{UsedPercent: 60.0},
	}

	c := NewWithConfig(&Config{Collector: mock})
	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics.CPUPercent)
	assert.Equal(t, 0.0, metrics.CPUIOWaitPercent)
	assert.Equal(t, 45.0, metrics.MemoryPercent)
}

// TestCollect_MemoryFailure_ReturnsZero - memory fails, returns 0
func TestCollect_MemoryFailure_ReturnsZero(t *testing.T) {
	mock := &mockSystemCollector{
		cpuStats:  CPUStats{User: 100, System: 50, Idle: 840, Iowait: 10},
		memErr:    errors.New("memory error"),
		swapStats: SwapStats{UsedPercent: 10.0},
		loadStats: LoadStats{Load1: 1.0, Load5: 0.8, Load15: 0.5},
		diskStats: DiskStats{UsedPercent: 60.0},
	}

	c := NewWithConfig(&Config{Collector: mock})
	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics.MemoryPercent)
	assert.Equal(t, 10.0, metrics.SwapUsagePercent)
}

// TestCollect_LoadAvgFailure_ReturnsZero - load avg fails, returns 0
func TestCollect_LoadAvgFailure_ReturnsZero(t *testing.T) {
	mock := &mockSystemCollector{
		cpuStats:  CPUStats{User: 100, System: 50, Idle: 840, Iowait: 10},
		memStats:  MemoryStats{UsedPercent: 45.0},
		swapStats: SwapStats{UsedPercent: 10.0},
		loadErr:   errors.New("load error"),
		diskStats: DiskStats{UsedPercent: 60.0},
	}

	c := NewWithConfig(&Config{Collector: mock})
	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics.LoadAvg1)
	assert.Equal(t, 0.0, metrics.LoadAvg5)
	assert.Equal(t, 0.0, metrics.LoadAvg15)
}

// TestCollect_SwapFailure_ReturnsZero - swap fails, returns 0
func TestCollect_SwapFailure_ReturnsZero(t *testing.T) {
	mock := &mockSystemCollector{
		cpuStats:  CPUStats{User: 100, System: 50, Idle: 840, Iowait: 10},
		memStats:  MemoryStats{UsedPercent: 45.0},
		swapErr:   errors.New("swap error"),
		loadStats: LoadStats{Load1: 1.0, Load5: 0.8, Load15: 0.5},
		diskStats: DiskStats{UsedPercent: 60.0},
	}

	c := NewWithConfig(&Config{Collector: mock})
	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics.SwapUsagePercent)
	assert.Equal(t, 45.0, metrics.MemoryPercent)
}

// TestCollect_DiskFailure_ReturnsZero - disk fails, returns 0
func TestCollect_DiskFailure_ReturnsZero(t *testing.T) {
	mock := &mockSystemCollector{
		cpuStats:  CPUStats{User: 100, System: 50, Idle: 840, Iowait: 10},
		memStats:  MemoryStats{UsedPercent: 45.0},
		swapStats: SwapStats{UsedPercent: 10.0},
		loadStats: LoadStats{Load1: 1.0, Load5: 0.8, Load15: 0.5},
		diskErr:   errors.New("disk error"),
	}

	c := NewWithConfig(&Config{Collector: mock})
	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics.DiskUsagePercent)
	assert.Equal(t, 45.0, metrics.MemoryPercent)
}

// TestCollect_AllFailures_ReturnsZeroMetrics - all fail, returns all zeros with nil error
func TestCollect_AllFailures_ReturnsZeroMetrics(t *testing.T) {
	mock := &mockSystemCollector{
		cpuErr:  errors.New("cpu error"),
		memErr:  errors.New("memory error"),
		swapErr: errors.New("swap error"),
		loadErr: errors.New("load error"),
		diskErr: errors.New("disk error"),
	}

	c := NewWithConfig(&Config{Collector: mock})
	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics.CPUPercent)
	assert.Equal(t, 0.0, metrics.CPUIOWaitPercent)
	assert.Equal(t, 0.0, metrics.MemoryPercent)
	assert.Equal(t, 0.0, metrics.LoadAvg1)
	assert.Equal(t, 0.0, metrics.LoadAvg5)
	assert.Equal(t, 0.0, metrics.LoadAvg15)
	assert.Equal(t, 0.0, metrics.SwapUsagePercent)
	assert.Equal(t, 0.0, metrics.DiskUsagePercent)
}
