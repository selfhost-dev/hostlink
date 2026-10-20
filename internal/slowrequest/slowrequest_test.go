package slowrequest

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/internal/testutil"
)

func request(t *testing.T, method, rawURL string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	require.NoError(t, err)
	return req
}

func TestReport_FastRequest_IsSilentAndUncounted(t *testing.T) {
	counts := NewCounts()
	r := NewWithCounts(time.Second, counts)
	req := request(t, http.MethodPost, "https://cp.example/api/v1/agents/agt_1/heartbeat")

	var got error
	output := testutil.CaptureLogs(t, func() {
		got = r.Report(req, 999*time.Millisecond, nil)
	})

	assert.NoError(t, got)
	assert.Empty(t, output)
	assert.Empty(t, counts.Drain())
}

func TestReport_SlowSuccess_WarnsOnceWithoutQueryString(t *testing.T) {
	counts := NewCounts()
	r := NewWithCounts(time.Second, counts)
	req := request(t, http.MethodGet, "https://cp.example/api/v1/tasks?token=super-secret")

	var got error
	output := testutil.CaptureLogs(t, func() {
		got = r.Report(req, 1500*time.Millisecond, nil)
	})

	assert.NoError(t, got)
	assert.Equal(t, 1, strings.Count(output, "slow control-plane request"), output)
	assert.Contains(t, output, "endpoint=/api/v1/tasks method=GET duration_ms=1500")
	assert.NotContains(t, output, "super-secret")
}

func TestReport_SlowFailure_AnnotatesTheErrorInsteadOfLogging(t *testing.T) {
	counts := NewCounts()
	r := NewWithCounts(time.Second, counts)
	req := request(t, http.MethodPost, "https://cp.example/api/v1/agents/agt_1/heartbeat")
	cause := errors.New("context deadline exceeded")

	var got error
	output := testutil.CaptureLogs(t, func() {
		got = r.Report(req, 10012*time.Millisecond, cause)
	})

	// The caller's loop logs the error once; the reporter adds the duration
	// to it instead of writing a second line.
	assert.Empty(t, output)
	require.Error(t, got)
	assert.Equal(t, "context deadline exceeded (after 10012ms)", got.Error())
	assert.ErrorIs(t, got, cause)
	assert.Equal(t, []Count{{Endpoint: "/api/v1/agents/:id/heartbeat", Method: "POST", Count: 1, MaxMs: 10012}}, counts.Drain())
}

func TestReport_FastFailure_ReturnsTheErrorUnchanged(t *testing.T) {
	r := NewWithCounts(time.Second, NewCounts())
	cause := errors.New("connection refused")

	got := r.Report(request(t, http.MethodGet, "https://cp.example/api/v1/tasks"), 5*time.Millisecond, cause)

	assert.Same(t, cause, got)
}

func TestEndpoint_GroupsIdentifiersOutOfThePath(t *testing.T) {
	cases := map[string]string{
		"/api/v1/agents/agt_01abc/heartbeat":      "/api/v1/agents/:id/heartbeat",
		"/api/v1/agents/agt_01abc/metrics":        "/api/v1/agents/:id/metrics",
		"/api/v1/agents/agt_01abc/credentials":    "/api/v1/agents/:id/credentials",
		"/api/v1/agents/agt_01abc/update":         "/api/v1/agents/:id/update",
		"/api/v1/agents/register":                 "/api/v1/agents/register",
		"/api/v1/agents/instances/ci_1/heartbeat": "/api/v1/agents/instances/:id/heartbeat",
		"/api/v1/tasks":                           "/api/v1/tasks",
		"/api/v1/tasks/tsk_01xyz":                 "/api/v1/tasks/:id",
		"/api/v1/agents/agt_01abc/heartbeat/":     "/api/v1/agents/:id/heartbeat/",
	}
	for path, want := range cases {
		assert.Equal(t, want, Endpoint(path), path)
	}
}

func TestCounts_DrainSumsAndKeepsTheMaxThenEmpties(t *testing.T) {
	counts := NewCounts()
	r := NewWithCounts(time.Second, counts)
	heartbeat := func(agent string) *http.Request {
		return request(t, http.MethodPost, "https://cp.example/api/v1/agents/"+agent+"/heartbeat")
	}

	testutil.CaptureLogs(t, func() {
		_ = r.Report(heartbeat("agt_a"), 2*time.Second, nil)
		_ = r.Report(heartbeat("agt_b"), 7*time.Second, nil)
		_ = r.Report(request(t, http.MethodGet, "https://cp.example/api/v1/tasks"), 3*time.Second, nil)
	})

	assert.Equal(t, []Count{
		{Endpoint: "/api/v1/agents/:id/heartbeat", Method: "POST", Count: 2, MaxMs: 7000},
		{Endpoint: "/api/v1/tasks", Method: "GET", Count: 1, MaxMs: 3000},
	}, counts.Drain())
	assert.Empty(t, counts.Drain(), "a drain empties the counts")
}

func TestCounts_RestoreMergesBackAnUndeliveredDrain(t *testing.T) {
	counts := NewCounts()
	counts.Restore([]Count{{Endpoint: "/api/v1/tasks", Method: "GET", Count: 2, MaxMs: 4000}})
	counts.Restore([]Count{{Endpoint: "/api/v1/tasks", Method: "GET", Count: 1, MaxMs: 9000}})

	assert.Equal(t, []Count{{Endpoint: "/api/v1/tasks", Method: "GET", Count: 3, MaxMs: 9000}}, counts.Drain())
}

func TestCounts_AreBoundedByOverflowingIntoOther(t *testing.T) {
	counts := NewCounts()
	for i := 0; i < MaxEndpoints+5; i++ {
		counts.record(fmt.Sprintf("/api/v1/unknown%d", i), http.MethodGet, 3000)
	}

	drained := counts.Drain()
	assert.Len(t, drained, MaxEndpoints+1)
	last := drained[len(drained)-1]
	assert.Equal(t, Count{Endpoint: OtherEndpoint, Method: "", Count: 5, MaxMs: 3000}, last)
}

func TestCounts_AreSafeForConcurrentUse(t *testing.T) {
	counts := NewCounts()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			counts.record("/api/v1/tasks", http.MethodGet, 2500)
		}()
	}
	wg.Wait()

	assert.Equal(t, []Count{{Endpoint: "/api/v1/tasks", Method: "GET", Count: 50, MaxMs: 2500}}, counts.Drain())
}
