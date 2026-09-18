package kafkametrics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"hostlink/domain/credential"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mutableMetricsServer serves a Prometheus text body that the test can swap
// between scrapes, so the collector's cumulative-counter rate math can be
// exercised across consecutive Collect calls.
func mutableMetricsServer(t *testing.T) (*httptest.Server, func(string)) {
	t.Helper()
	var (
		mu   sync.Mutex
		body string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprint(w, body)
	}))
	return srv, func(b string) {
		mu.Lock()
		body = b
		mu.Unlock()
	}
}

func dump(bytesTotal, msgTotal, reqTotal float64) string {
	return fmt.Sprintf(
		"kafka_network_io_bytes_total %f\n"+
			"kafka_message_count_total %f\n"+
			"kafka_request_count_total %f\n"+
			"kafka_controller_active_count 1\n"+
			"kafka_partition_offline_count 0\n"+
			"kafka_partition_total_count 12\n",
		bytesTotal, msgTotal, reqTotal)
}

func TestCollect_RatesCumulativeCounters(t *testing.T) {
	srv, setBody := mutableMetricsServer(t)
	defer srv.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := base
	c := NewWithEndpoint(srv.URL).(*collector)
	c.now = func() time.Time { return clock }

	// First scrape establishes the baseline — rates must be 0, gauges populated.
	setBody(dump(1000, 500, 200))
	m, err := c.Collect(credential.Credential{})
	require.NoError(t, err)
	assert.True(t, m.Up)
	assert.Equal(t, float64(0), m.BytesInPerSec, "first scrape has no baseline → 0")
	assert.Equal(t, float64(0), m.MessagesInPerSec)
	assert.Equal(t, float64(0), m.TotalProduceRequestsPerSec)
	assert.Equal(t, 1, m.ActiveControllerCount, "gauges are reported as-is on the first scrape")
	assert.Equal(t, 12, m.GlobalPartitionCount)

	// 10s later the counters grew → per-second rate = delta / 10.
	clock = base.Add(10 * time.Second)
	setBody(dump(1000+2000, 500+100, 200+50))
	m, err = c.Collect(credential.Credential{})
	require.NoError(t, err)
	assert.Equal(t, float64(200), m.BytesInPerSec, "2000 bytes / 10s")
	assert.Equal(t, float64(10), m.MessagesInPerSec, "100 msgs / 10s")
	assert.Equal(t, float64(5), m.TotalProduceRequestsPerSec, "50 requests / 10s")

	// A counter reset (broker restart → totals drop) must not produce a negative
	// or huge spike — the rate resets to 0.
	clock = base.Add(20 * time.Second)
	setBody(dump(10, 5, 2))
	m, err = c.Collect(credential.Credential{})
	require.NoError(t, err)
	assert.Equal(t, float64(0), m.BytesInPerSec, "counter reset → 0, never negative")
	assert.Equal(t, float64(0), m.MessagesInPerSec)
	assert.Equal(t, float64(0), m.TotalProduceRequestsPerSec)
}

func TestCollect_BrokerDownReportsUpFalse(t *testing.T) {
	// Nothing listening → Collect returns Up=false and no error, so the heartbeat
	// still reports liveness rather than dropping the whole beat.
	c := NewWithEndpoint("http://127.0.0.1:0/metrics")
	m, err := c.Collect(credential.Credential{})
	require.NoError(t, err)
	assert.False(t, m.Up)
}
