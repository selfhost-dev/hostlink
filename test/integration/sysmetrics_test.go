//go:build integration
// +build integration

package integration

import (
	"context"
	"testing"
	"time"

	"hostlink/internal/sysmetrics"

	"github.com/stretchr/testify/assert"
)

// TestSysmetrics_CollectsRealMetrics - collects real system metrics including CPU IO wait
func TestSysmetrics_CollectsRealMetrics(t *testing.T) {
	c := sysmetrics.New()
	ctx := context.Background()

	// First collection - baseline (CPU returns 0)
	metrics1, err := c.Collect(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics1.CPUPercent)
	assert.Equal(t, 0.0, metrics1.CPUIOWaitPercent)

	// Wait for CPU delta
	time.Sleep(100 * time.Millisecond)

	// Second collection - real values
	metrics2, err := c.Collect(ctx)
	assert.NoError(t, err)

	// CPU values should be between 0 and 100
	assert.GreaterOrEqual(t, metrics2.CPUPercent, 0.0)
	assert.LessOrEqual(t, metrics2.CPUPercent, 100.0)
	assert.GreaterOrEqual(t, metrics2.CPUIOWaitPercent, 0.0)
	assert.LessOrEqual(t, metrics2.CPUIOWaitPercent, 100.0)

	// Other metrics should be valid
	assert.GreaterOrEqual(t, metrics2.MemoryPercent, 0.0)
	assert.LessOrEqual(t, metrics2.MemoryPercent, 100.0)
	assert.GreaterOrEqual(t, metrics2.DiskUsagePercent, 0.0)
	assert.LessOrEqual(t, metrics2.DiskUsagePercent, 100.0)
	assert.GreaterOrEqual(t, metrics2.SwapUsagePercent, 0.0)
	assert.GreaterOrEqual(t, metrics2.LoadAvg1, 0.0)
	assert.GreaterOrEqual(t, metrics2.LoadAvg5, 0.0)
	assert.GreaterOrEqual(t, metrics2.LoadAvg15, 0.0)
}
