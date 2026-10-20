package apiserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/slowrequest"
	"hostlink/internal/telemetry/telemetrytest"
	"hostlink/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient builds a client pointed at serverURL through an injectable
// Config, with a throwaway agent identity so requests can be signed.
func newTestClient(t *testing.T, serverURL string, timeout, threshold time.Duration) *client {
	t.Helper()

	stateDir, keyPath := testutil.SetupAgentIdentity(t)

	c, err := NewClient(Config{
		baseURL:              serverURL,
		privateKeyPath:       keyPath,
		agentStatePath:       stateDir,
		timeout:              timeout,
		slowRequestThreshold: threshold,
	})
	require.NoError(t, err)

	return c
}

var durationMsPattern = regexp.MustCompile(`duration_ms=(\d+)`)

// reportedDurationMs extracts the duration_ms value from a warning line.
func reportedDurationMs(t *testing.T, output string) int {
	t.Helper()

	match := durationMsPattern.FindStringSubmatch(output)
	require.NotNil(t, match, "expected duration_ms in output: %s", output)

	value, err := strconv.Atoi(match[1])
	require.NoError(t, err)

	return value
}

func TestDo_SlowRequest_LogsSingleWarning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	const threshold = 50 * time.Millisecond
	c := newTestClient(t, server.URL, 5*time.Second, threshold)

	var result map[string]any
	output := testutil.CaptureLogs(t, func() {
		err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", &result)
		require.NoError(t, err)
	})

	assert.Equal(t, 1, strings.Count(output, "slow control-plane request"), "expected exactly one warning, got: %s", output)
	assert.Contains(t, output, "endpoint=/api/v1/agents/test-agent-123/heartbeat")
	assert.Contains(t, output, "method=GET")
	assert.GreaterOrEqual(t, reportedDurationMs(t, output), int(threshold.Milliseconds()))
}

func TestDo_FastRequest_LogsNoWarning(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	const threshold = 2 * time.Second
	c := newTestClient(t, server.URL, 5*time.Second, threshold)

	var result map[string]any
	output := testutil.CaptureLogs(t, func() {
		err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", &result)
		require.NoError(t, err)
	})

	assert.NotContains(t, output, "slow control-plane request", "expected no warning, got: %s", output)
}

func TestDo_SlowRequest_ExcludesQueryString(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	c := newTestClient(t, server.URL, 5*time.Second, 50*time.Millisecond)

	output := testutil.CaptureLogs(t, func() {
		err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat?token=super-secret", nil)
		require.NoError(t, err)
	})

	assert.Equal(t, 1, strings.Count(output, "slow control-plane request"), "expected exactly one warning, got: %s", output)
	assert.Contains(t, output, "endpoint=/api/v1/agents/test-agent-123/heartbeat")
	assert.NotContains(t, output, "super-secret")
	assert.NotContains(t, output, "token=")
}

func TestDo_SlowRequest_ReportsTelemetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	telemetryPath := filepath.Join(t.TempDir(), "telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)

	const threshold = 50 * time.Millisecond
	c := newTestClient(t, server.URL, 5*time.Second, threshold)

	err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
	require.NoError(t, err)

	entries := telemetrytest.ReadEntries(t, telemetryPath)
	metric := telemetrytest.FindEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.api.request.slow"
	})

	require.NotEmpty(t, metric, "expected a slow request metric, got: %v", entries)
	assert.Equal(t, "/api/v1/agents/:id/heartbeat", metric["endpoint"], "the agent ID is grouped out of the metric")
	assert.Equal(t, "GET", metric["method"])

	value, ok := metric["value"].(float64)
	require.True(t, ok, "expected a numeric metric value, got: %v", metric["value"])
	assert.GreaterOrEqual(t, int64(value), int64(threshold.Milliseconds()))
}

func TestDo_Timeout_ReturnsDeadlineError(t *testing.T) {
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-released
	}))
	defer func() {
		close(released)
		server.Close()
	}()

	const timeout = 300 * time.Millisecond
	c := newTestClient(t, server.URL, timeout, time.Second)

	started := time.Now()
	err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
	elapsed := time.Since(started)

	require.Error(t, err, "expected a timeout error, got a silent success")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "expected a deadline error, got: %v", err)
	assert.GreaterOrEqual(t, elapsed, timeout)
	assert.Less(t, elapsed, 5*time.Second, "expected the request to fail on the client timeout")
}

func TestDo_ReportsSlowRequestForEveryEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/credentials") {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	c := newTestClient(t, server.URL, 5*time.Second, 50*time.Millisecond)

	output := testutil.CaptureLogs(t, func() {
		_, err := c.Heartbeat(context.Background(), "test-agent-123", nil)
		require.NoError(t, err)

		err = c.PushMetrics(context.Background(), metricsPayload())
		require.NoError(t, err)

		_, err = c.GetMetricsCreds(context.Background(), "test-agent-123")
		require.NoError(t, err)
	})

	assert.Equal(t, 3, strings.Count(output, "slow control-plane request"), "expected one warning per endpoint, got: %s", output)
	assert.Contains(t, output, "endpoint=/api/v1/agents/test-agent-123/heartbeat")
	assert.Contains(t, output, "endpoint=/api/v1/agents/test-agent-123/metrics")
	assert.Contains(t, output, "endpoint=/api/v1/agents/test-agent-123/credentials")
}

// metricsPayload is a minimal metrics payload for PushMetrics.
func metricsPayload() domainmetrics.MetricPayload {
	return domainmetrics.MetricPayload{
		Version:     "1.0",
		TimestampMs: time.Now().UnixMilli(),
		Resource: domainmetrics.Resource{
			AgentID:  "test-agent-123",
			HostName: "test-host",
		},
	}
}

func TestNewDefaultClient_AppliesConfiguredSlowRequestThreshold(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	testutil.SetupAgentIdentity(t)
	t.Setenv("SH_CONTROL_PLANE_URL", server.URL)
	t.Setenv("HOSTLINK_API_SLOW_REQUEST_THRESHOLD", "50ms")

	c, err := NewDefaultClient()
	require.NoError(t, err)

	output := testutil.CaptureLogs(t, func() {
		var result map[string]any
		err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", &result)
		require.NoError(t, err)
	})

	assert.Contains(t, output, "slow control-plane request", "expected the configured threshold to be applied")
}

func TestNewDefaultClient_UsesDefaultSlowRequestThreshold(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	testutil.SetupAgentIdentity(t)
	t.Setenv("SH_CONTROL_PLANE_URL", server.URL)
	t.Setenv("HOSTLINK_API_SLOW_REQUEST_THRESHOLD", "")

	c, err := NewDefaultClient()
	require.NoError(t, err)

	output := testutil.CaptureLogs(t, func() {
		var result map[string]any
		err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", &result)
		require.NoError(t, err)
	})

	assert.NotContains(t, output, "slow control-plane request", "expected the default threshold to be two seconds")
}

// A slow failure is logged once, by the caller's loop: the reporter adds the
// duration to the error instead of writing its own line.
func TestDo_SlowFailingRequest_AddsTheDurationToTheErrorInsteadOfLogging(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL, 5*time.Second, 50*time.Millisecond)

	var err error
	output := testutil.CaptureLogs(t, func() {
		err = c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
	})

	require.Error(t, err)
	assert.Empty(t, output, "the reporter must not log a failure the caller logs")
	assert.Contains(t, err.Error(), "unexpected status code: 500")
	assert.Regexp(t, `\(after \d+ms\)$`, err.Error())

	var slow *slowrequest.Error
	require.ErrorAs(t, err, &slow)
	assert.GreaterOrEqual(t, slow.Elapsed, 50*time.Millisecond)
}

func TestDo_SlowClientError_StaysAnAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}))
	defer server.Close()

	c := newTestClient(t, server.URL, 5*time.Second, 50*time.Millisecond)

	err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)

	var apiErr *APIError
	require.ErrorAs(t, err, &apiErr, "callers that branch on APIError must still see it")
	assert.True(t, apiErr.IsNotFound())
}

func TestDo_FastFailingRequest_ReturnsTheErrorUnannotated(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	c := newTestClient(t, server.URL, 5*time.Second, 2*time.Second)

	err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)

	require.Error(t, err)
	assert.NotContains(t, err.Error(), "(after")
}
