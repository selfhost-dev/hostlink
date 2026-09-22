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

// dump renders a Prometheus body using AutoMQ's REAL series + labels (network
// throughput split by `direction`, requests by `type`, the per-stream S3 byte
// counters), so the collector's mapping is exercised against names that actually
// exist on 1.7.4. There is deliberately NO consumer-group series here: the broker
// exports none (selfhost#2854) — lag comes from the admin API, see lag_test.go.
func dump(netIn, netOut, msgTotal, produce, fetch, s3Up, s3Down float64) string {
	return fmt.Sprintf(
		"kafka_broker_network_io_bytes_total{direction=\"in\"} %f\n"+
			"kafka_broker_network_io_bytes_total{direction=\"out\"} %f\n"+
			"kafka_message_count_total %f\n"+
			"kafka_request_count_total{type=\"Produce\"} %f\n"+
			"kafka_request_count_total{type=\"Fetch\"} %f\n"+
			"kafka_stream_upload_size_bytes_total %f\n"+
			"kafka_stream_download_size_bytes_total %f\n"+
			"kafka_controller_active_count 1\n"+
			"kafka_partition_offline_count 0\n"+
			"kafka_partition_total_count 12\n",
		netIn, netOut, msgTotal, produce, fetch, s3Up, s3Down)
}

func TestCollect_RatesCumulativeCounters(t *testing.T) {
	srv, setBody := mutableMetricsServer(t)
	defer srv.Close()

	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	clock := base
	c := NewWithEndpoint(srv.URL).(*collector)
	c.now = func() time.Time { return clock }

	// First scrape establishes the baseline — rates must be 0, gauges populated.
	setBody(dump(1000, 500, 500, 200, 100, 5000, 2000))
	m, err := c.Collect(credential.Credential{})
	require.NoError(t, err)
	assert.True(t, m.Up)
	assert.Equal(t, float64(0), m.BytesInPerSec, "first scrape has no baseline → 0")
	assert.Equal(t, float64(0), m.BytesOutPerSec)
	assert.Equal(t, float64(0), m.MessagesInPerSec)
	assert.Equal(t, float64(0), m.TotalProduceRequestsPerSec)
	assert.Equal(t, float64(0), m.TotalFetchRequestsPerSec)
	assert.Equal(t, float64(0), m.S3UploadSizeBytesPerSec)
	assert.Equal(t, 1, m.ActiveControllerCount, "gauges are reported as-is on the first scrape")
	assert.Equal(t, 12, m.GlobalPartitionCount)
	// Lag is a derived gauge (log-end − group-commit), reported as-is each scrape.
	assert.Nil(t, m.MaxConsumerGroupLag, "no lag source → consumer-group keys are omitted, never 0")
	assert.Nil(t, m.ConsumerGroupCount)
	assert.Empty(t, m.ConsumerLagSource)

	// 10s later the counters grew → per-second rate = delta / 10, split by label.
	clock = base.Add(10 * time.Second)
	setBody(dump(1000+2000, 500+500, 500+100, 200+50, 100+30, 5000+4000, 2000+1000))
	m, err = c.Collect(credential.Credential{})
	require.NoError(t, err)
	assert.Equal(t, float64(200), m.BytesInPerSec, "2000 in-bytes / 10s")
	assert.Equal(t, float64(50), m.BytesOutPerSec, "500 out-bytes / 10s (was hardcoded 0)")
	assert.Equal(t, float64(10), m.MessagesInPerSec, "100 msgs / 10s")
	assert.Equal(t, float64(5), m.TotalProduceRequestsPerSec, "50 produce / 10s")
	assert.Equal(t, float64(3), m.TotalFetchRequestsPerSec, "30 fetch / 10s (was hardcoded 0)")
	assert.Equal(t, float64(400), m.S3UploadSizeBytesPerSec, "4000 S3-upload bytes / 10s (was 0)")
	assert.Equal(t, float64(100), m.S3DownloadSizeBytesPerSec, "1000 S3-download bytes / 10s (was 0)")

	// A counter reset (broker restart → totals drop) must not produce a negative
	// or huge spike — the rate resets to 0.
	clock = base.Add(20 * time.Second)
	setBody(dump(10, 5, 5, 2, 1, 50, 20))
	m, err = c.Collect(credential.Credential{})
	require.NoError(t, err)
	assert.Equal(t, float64(0), m.BytesInPerSec, "counter reset → 0, never negative")
	assert.Equal(t, float64(0), m.BytesOutPerSec)
	assert.Equal(t, float64(0), m.MessagesInPerSec)
	assert.Equal(t, float64(0), m.TotalProduceRequestsPerSec)
	assert.Equal(t, float64(0), m.S3UploadSizeBytesPerSec)
}

func TestCollect_BrokerDownReportsUpFalse(t *testing.T) {
	// Nothing listening → Collect returns Up=false and no error, so the heartbeat
	// still reports liveness rather than dropping the whole beat.
	c := NewWithEndpoint("http://127.0.0.1:0/metrics")
	m, err := c.Collect(credential.Credential{})
	require.NoError(t, err)
	assert.False(t, m.Up)
}
