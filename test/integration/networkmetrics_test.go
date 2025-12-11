//go:build integration
// +build integration

package integration

import (
	"context"
	"testing"
	"time"

	"hostlink/internal/networkmetrics"

	"github.com/stretchr/testify/assert"
)

// TestNetworkmetrics_CollectsRealMetrics - collects real network metrics
func TestNetworkmetrics_CollectsRealMetrics(t *testing.T) {
	c := networkmetrics.New()
	ctx := context.Background()

	// First collection - baseline (returns 0)
	metrics1, err := c.Collect(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics1.RecvBytesPerSec)
	assert.Equal(t, 0.0, metrics1.SentBytesPerSec)

	// Wait for delta
	time.Sleep(100 * time.Millisecond)

	// Second collection - real values
	metrics2, err := c.Collect(ctx)
	assert.NoError(t, err)

	// Network values should be >= 0
	assert.GreaterOrEqual(t, metrics2.RecvBytesPerSec, 0.0)
	assert.GreaterOrEqual(t, metrics2.SentBytesPerSec, 0.0)
}
