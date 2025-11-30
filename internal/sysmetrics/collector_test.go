package sysmetrics

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockExecutor struct {
	outputs map[string]string
	errs    map[string]error
}

func (m *mockExecutor) Execute(ctx context.Context, command string) (string, error) {
	if err, ok := m.errs[command]; ok && err != nil {
		return "", err
	}
	return m.outputs[command], nil
}

func TestCollectCPU_ParsesTopOutput(t *testing.T) {
	mock := &mockExecutor{
		outputs: map[string]string{
			cpuCommand: "0.0",
		},
	}
	c := &collector{executor: mock}

	cpu, err := c.collectCPU(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, cpu)
}

func TestCollectCPU_CalculatesUsageFromIdle(t *testing.T) {
	mock := &mockExecutor{
		outputs: map[string]string{
			cpuCommand: "24.5",
		},
	}
	c := &collector{executor: mock}

	cpu, err := c.collectCPU(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 24.5, cpu)
}

func TestCollectCPU_ExecutorError(t *testing.T) {
	mock := &mockExecutor{
		errs: map[string]error{
			cpuCommand: errors.New("command failed"),
		},
	}
	c := &collector{executor: mock}

	_, err := c.collectCPU(context.Background())

	assert.Error(t, err)
}

func TestCollectMemory_ParsesFreeOutput(t *testing.T) {
	mock := &mockExecutor{
		outputs: map[string]string{
			memoryCommand: "13.24",
		},
	}
	c := &collector{executor: mock}

	mem, err := c.collectMemory(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 13.24, mem)
}

func TestCollectMemory_ExecutorError(t *testing.T) {
	mock := &mockExecutor{
		errs: map[string]error{
			memoryCommand: errors.New("command failed"),
		},
	}
	c := &collector{executor: mock}

	_, err := c.collectMemory(context.Background())

	assert.Error(t, err)
}

func TestCollectLoadAvg_ParsesProcLoadavg(t *testing.T) {
	mock := &mockExecutor{
		outputs: map[string]string{
			loadAvgCommand: "0.15 0.10 0.05",
		},
	}
	c := &collector{executor: mock}

	l1, l5, l15, err := c.collectLoadAvg(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.15, l1)
	assert.Equal(t, 0.10, l5)
	assert.Equal(t, 0.05, l15)
}

func TestCollectLoadAvg_ExecutorError(t *testing.T) {
	mock := &mockExecutor{
		errs: map[string]error{
			loadAvgCommand: errors.New("command failed"),
		},
	}
	c := &collector{executor: mock}

	_, _, _, err := c.collectLoadAvg(context.Background())

	assert.Error(t, err)
}

func TestCollectSwap_ParsesFreeOutput(t *testing.T) {
	mock := &mockExecutor{
		outputs: map[string]string{
			swapCommand: "25.50",
		},
	}
	c := &collector{executor: mock}

	swap, err := c.collectSwap(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 25.50, swap)
}

func TestCollectSwap_ZeroWhenNoSwap(t *testing.T) {
	mock := &mockExecutor{
		outputs: map[string]string{
			swapCommand: "0",
		},
	}
	c := &collector{executor: mock}

	swap, err := c.collectSwap(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, swap)
}

func TestCollectSwap_ExecutorError(t *testing.T) {
	mock := &mockExecutor{
		errs: map[string]error{
			swapCommand: errors.New("command failed"),
		},
	}
	c := &collector{executor: mock}

	_, err := c.collectSwap(context.Background())

	assert.Error(t, err)
}

func TestCollect_ReturnsAllMetrics(t *testing.T) {
	diskPath := "/selfhostdev/postgresql"
	diskCmd := "df " + diskPath + " | tail -1 | awk '{print $5}' | tr -d '%'"
	mock := &mockExecutor{
		outputs: map[string]string{
			cpuCommand:     "15.5",
			memoryCommand:  "42.30",
			loadAvgCommand: "1.25 0.80 0.50",
			swapCommand:    "10.00",
			diskCmd:        "25",
		},
	}
	c := &collector{executor: mock, diskPath: diskPath}

	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 15.5, metrics.CPUPercent)
	assert.Equal(t, 42.30, metrics.MemoryPercent)
	assert.Equal(t, 1.25, metrics.LoadAvg1)
	assert.Equal(t, 0.80, metrics.LoadAvg5)
	assert.Equal(t, 0.50, metrics.LoadAvg15)
	assert.Equal(t, 10.00, metrics.SwapUsagePercent)
	assert.Equal(t, 25.0, metrics.DiskUsagePercent)
}

func TestCollect_PartialFailure(t *testing.T) {
	diskPath := "/selfhostdev/postgresql"
	diskCmd := "df " + diskPath + " | tail -1 | awk '{print $5}' | tr -d '%'"
	mock := &mockExecutor{
		outputs: map[string]string{
			memoryCommand:  "42.30",
			loadAvgCommand: "1.25 0.80 0.50",
			swapCommand:    "10.00",
			diskCmd:        "25",
		},
		errs: map[string]error{
			cpuCommand: errors.New("cpu command failed"),
		},
	}
	c := &collector{executor: mock, diskPath: diskPath}

	metrics, err := c.Collect(context.Background())

	assert.Error(t, err)
	assert.Equal(t, 0.0, metrics.CPUPercent)
	assert.Equal(t, 42.30, metrics.MemoryPercent)
	assert.Equal(t, 1.25, metrics.LoadAvg1)
	assert.Equal(t, 0.80, metrics.LoadAvg5)
	assert.Equal(t, 0.50, metrics.LoadAvg15)
	assert.Equal(t, 10.00, metrics.SwapUsagePercent)
	assert.Equal(t, 25.0, metrics.DiskUsagePercent)
}

// TestCollectDisk_ParsesDfOutput - verifies disk% from df command
func TestCollectDisk_ParsesDfOutput(t *testing.T) {
	diskPath := "/selfhostdev/postgresql"
	c := &collector{
		executor: &mockExecutor{
			outputs: map[string]string{
				"df " + diskPath + " | tail -1 | awk '{print $5}' | tr -d '%'": "20",
			},
		},
		diskPath: diskPath,
	}

	disk, err := c.collectDisk(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 20.0, disk)
}

// TestCollectDisk_ExecutorError - verifies error handling when command fails
func TestCollectDisk_ExecutorError(t *testing.T) {
	diskPath := "/selfhostdev/postgresql"
	c := &collector{
		executor: &mockExecutor{
			errs: map[string]error{
				"df " + diskPath + " | tail -1 | awk '{print $5}' | tr -d '%'": errors.New("command failed"),
			},
		},
		diskPath: diskPath,
	}

	_, err := c.collectDisk(context.Background())

	assert.Error(t, err)
}

// TestDiskCommand_FormatsPathCorrectly - verifies disk command includes correct path
func TestDiskCommand_FormatsPathCorrectly(t *testing.T) {
	diskPath := "/selfhostdev/postgresql"
	c := &collector{diskPath: diskPath}

	cmd := c.diskCommand()

	assert.Contains(t, cmd, diskPath)
	assert.Equal(t, "df /selfhostdev/postgresql | tail -1 | awk '{print $5}' | tr -d '%'", cmd)
}
