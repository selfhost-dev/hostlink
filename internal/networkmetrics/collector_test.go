package networkmetrics

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

type mockNetworkCollector struct {
	stats NetworkStats
	err   error
}

func (m *mockNetworkCollector) NetworkIO(ctx context.Context) (NetworkStats, error) {
	return m.stats, m.err
}

// TestCollect_FirstCollection_ReturnsZero - first collection stores baseline, returns 0
func TestCollect_FirstCollection_ReturnsZero(t *testing.T) {
	mock := &mockNetworkCollector{
		stats: NetworkStats{RecvBytes: 1000, SentBytes: 500},
	}

	c := NewWithConfig(&Config{Collector: mock})
	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics.RecvBytesPerSec)
	assert.Equal(t, 0.0, metrics.SentBytesPerSec)
}

// TestCollect_SecondCollection_ReturnsDelta - second collection calculates bytes per second
func TestCollect_SecondCollection_ReturnsDelta(t *testing.T) {
	mock := &mockNetworkCollector{}

	c := NewWithConfig(&Config{Collector: mock})

	// First collection - baseline
	mock.stats = NetworkStats{RecvBytes: 1000, SentBytes: 500}
	c.Collect(context.Background())

	// Second collection - delta
	mock.stats = NetworkStats{RecvBytes: 2000, SentBytes: 1500}

	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Greater(t, metrics.RecvBytesPerSec, 0.0)
	assert.Greater(t, metrics.SentBytesPerSec, 0.0)
}

// TestCollect_Failure_ReturnsZero - failure returns zero metrics with nil error
func TestCollect_Failure_ReturnsZero(t *testing.T) {
	mock := &mockNetworkCollector{
		err: errors.New("network error"),
	}

	c := NewWithConfig(&Config{Collector: mock})
	metrics, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, 0.0, metrics.RecvBytesPerSec)
	assert.Equal(t, 0.0, metrics.SentBytesPerSec)
}
