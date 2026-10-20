package taskreporter

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"hostlink/internal/telemetry/telemetrytest"
	"hostlink/internal/testutil"
)

func setupSlowTestReporter(t *testing.T, serverURL string, threshold time.Duration) *taskreporter {
	t.Helper()

	keys := setupTestKeys(t)
	reporter, err := New(&Config{
		AgentState:           setupTestAgentState(t, "test-agent-123"),
		PrivateKeyPath:       saveTestPrivateKey(t, t.TempDir(), keys.privateKey),
		ControlPlaneURL:      serverURL,
		Timeout:              5 * time.Second,
		SleepFunc:            func(time.Duration) {},
		SlowRequestThreshold: threshold,
	})
	if err != nil {
		t.Fatalf("failed to create test reporter: %v", err)
	}
	return reporter
}

func TestTaskReporter_ReportsASlowResultPost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	telemetryPath := filepath.Join(t.TempDir(), "telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
	reporter := setupSlowTestReporter(t, server.URL, 50*time.Millisecond)

	output := testutil.CaptureLogs(t, func() {
		if err := reporter.Report("tsk_01abc", &TaskResult{Status: "completed"}); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	if strings.Count(output, "slow control-plane request") != 1 {
		t.Errorf("expected exactly one slow request warning, got: %s", output)
	}
	if !strings.Contains(output, "endpoint=/api/v1/tasks/tsk_01abc method=PUT") {
		t.Errorf("expected the endpoint and method in the warning, got: %s", output)
	}

	metric := telemetrytest.FindEntry(telemetrytest.ReadEntries(t, telemetryPath), func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.api.request.slow"
	})
	if metric["endpoint"] != "/api/v1/tasks/:id" {
		t.Errorf("expected the task ID grouped out of the metric, got: %v", metric)
	}
}

func TestTaskReporter_AddsTheDurationToASlowFailedAttempt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	reporter := setupSlowTestReporter(t, server.URL, 50*time.Millisecond)

	var err error
	output := testutil.CaptureLogs(t, func() {
		err = reporter.Report("tsk_01abc", &TaskResult{Status: "completed"})
	})

	if err == nil {
		t.Fatal("expected the non-retryable 400 to fail")
	}
	if !regexp.MustCompile(`^unexpected status code: 400 \(after \d+ms\)$`).MatchString(err.Error()) {
		t.Errorf("expected the duration on the error, got: %v", err)
	}
	if strings.Contains(output, "slow control-plane request") {
		t.Errorf("a failed attempt is reported through its error, not a second line: %s", output)
	}
}

func TestTaskReporter_QuietOnAFastResultPost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reporter := setupSlowTestReporter(t, server.URL, 2*time.Second)

	output := testutil.CaptureLogs(t, func() {
		if err := reporter.Report("tsk_01abc", &TaskResult{Status: "completed"}); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	})

	if strings.Contains(output, "slow control-plane request") {
		t.Errorf("expected no warning, got: %s", output)
	}
}
