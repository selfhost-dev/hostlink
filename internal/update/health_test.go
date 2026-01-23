package update

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthChecker_WaitForHealth_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Ok: true, Version: "v1.0.0"})
	}))
	defer server.Close()

	hc := NewHealthChecker(HealthConfig{
		URL:           server.URL,
		TargetVersion: "v1.0.0",
		MaxRetries:    5,
		RetryInterval: 10 * time.Millisecond,
		InitialWait:   0,
		SleepFunc:     func(d time.Duration) {},
	})

	err := hc.WaitForHealth(context.Background())
	require.NoError(t, err)
}

func TestHealthChecker_WaitForHealth_RetriesOnHttpError(t *testing.T) {
	var attempts atomic.Int32

	// Server returns 503 initially, then succeeds
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		if count < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Ok: true, Version: "v1.0.0"})
	}))
	defer server.Close()

	hc := NewHealthChecker(HealthConfig{
		URL:           server.URL,
		TargetVersion: "v1.0.0",
		MaxRetries:    5,
		RetryInterval: 10 * time.Millisecond,
		InitialWait:   0,
		SleepFunc:     func(d time.Duration) {},
	})

	err := hc.WaitForHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(3), attempts.Load())
}

func TestHealthChecker_WaitForHealth_RetriesOnOkFalse(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if count < 3 {
			json.NewEncoder(w).Encode(HealthResponse{Ok: false, Version: "v1.0.0"})
		} else {
			json.NewEncoder(w).Encode(HealthResponse{Ok: true, Version: "v1.0.0"})
		}
	}))
	defer server.Close()

	hc := NewHealthChecker(HealthConfig{
		URL:           server.URL,
		TargetVersion: "v1.0.0",
		MaxRetries:    5,
		RetryInterval: 10 * time.Millisecond,
		InitialWait:   0,
		SleepFunc:     func(d time.Duration) {},
	})

	err := hc.WaitForHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(3), attempts.Load())
}

func TestHealthChecker_WaitForHealth_FailsAfterMaxRetries(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Ok: false, Version: "v1.0.0"})
	}))
	defer server.Close()

	hc := NewHealthChecker(HealthConfig{
		URL:           server.URL,
		TargetVersion: "v1.0.0",
		MaxRetries:    3,
		RetryInterval: 10 * time.Millisecond,
		InitialWait:   0,
		SleepFunc:     func(d time.Duration) {},
	})

	err := hc.WaitForHealth(context.Background())
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrHealthCheckFailed)
	// Should have tried 4 times (initial + 3 retries)
	assert.Equal(t, int32(4), attempts.Load())
}

func TestHealthChecker_WaitForHealth_RetriesOnVersionMismatch(t *testing.T) {
	var attempts atomic.Int32

	// Server returns old version for 2 attempts, then correct version
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if count < 3 {
			json.NewEncoder(w).Encode(HealthResponse{Ok: true, Version: "v1.0.0"})
		} else {
			json.NewEncoder(w).Encode(HealthResponse{Ok: true, Version: "v2.0.0"})
		}
	}))
	defer server.Close()

	hc := NewHealthChecker(HealthConfig{
		URL:           server.URL,
		TargetVersion: "v2.0.0",
		MaxRetries:    5,
		RetryInterval: 10 * time.Millisecond,
		InitialWait:   0,
		SleepFunc:     func(d time.Duration) {},
	})

	err := hc.WaitForHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(3), attempts.Load())
}

func TestHealthChecker_WaitForHealth_FailsOnVersionMismatch(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Ok: true, Version: "v1.0.0"})
	}))
	defer server.Close()

	hc := NewHealthChecker(HealthConfig{
		URL:           server.URL,
		TargetVersion: "v2.0.0", // Different from what server returns
		MaxRetries:    3,
		RetryInterval: 10 * time.Millisecond,
		InitialWait:   0,
		SleepFunc:     func(d time.Duration) {},
	})

	err := hc.WaitForHealth(context.Background())
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrHealthCheckFailed)
	// Should have exhausted all retries (initial + 3 retries = 4 attempts)
	assert.Equal(t, int32(4), attempts.Load())
}

func TestHealthChecker_WaitForHealth_RespectsContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Ok: false, Version: "v1.0.0"})
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())

	hc := NewHealthChecker(HealthConfig{
		URL:           server.URL,
		TargetVersion: "v1.0.0",
		MaxRetries:    10,
		RetryInterval: 10 * time.Millisecond,
		InitialWait:   0,
		SleepFunc: func(d time.Duration) {
			cancel() // Cancel context during sleep
		},
	})

	err := hc.WaitForHealth(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestHealthChecker_WaitForHealth_InitialWait(t *testing.T) {
	var sleepDurations []time.Duration

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(HealthResponse{Ok: true, Version: "v1.0.0"})
	}))
	defer server.Close()

	hc := NewHealthChecker(HealthConfig{
		URL:           server.URL,
		TargetVersion: "v1.0.0",
		MaxRetries:    5,
		RetryInterval: 100 * time.Millisecond,
		InitialWait:   500 * time.Millisecond,
		SleepFunc: func(d time.Duration) {
			sleepDurations = append(sleepDurations, d)
		},
	})

	err := hc.WaitForHealth(context.Background())
	require.NoError(t, err)

	// First sleep should be the initial wait
	require.GreaterOrEqual(t, len(sleepDurations), 1)
	assert.Equal(t, 500*time.Millisecond, sleepDurations[0])
}

func TestHealthChecker_DefaultConfig(t *testing.T) {
	hc := NewHealthChecker(HealthConfig{
		URL:           "http://localhost:8080/health",
		TargetVersion: "v1.0.0",
	})

	// Verify defaults
	assert.Equal(t, 5, hc.config.MaxRetries)
	assert.Equal(t, 5*time.Second, hc.config.RetryInterval)
	assert.Equal(t, 5*time.Second, hc.config.InitialWait)
}

func TestHealthChecker_WaitForHealth_HandlesInvalidJSON(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attempts.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if count < 2 {
			w.Write([]byte("not json"))
		} else {
			json.NewEncoder(w).Encode(HealthResponse{Ok: true, Version: "v1.0.0"})
		}
	}))
	defer server.Close()

	hc := NewHealthChecker(HealthConfig{
		URL:           server.URL,
		TargetVersion: "v1.0.0",
		MaxRetries:    5,
		RetryInterval: 10 * time.Millisecond,
		InitialWait:   0,
		SleepFunc:     func(d time.Duration) {},
	})

	err := hc.WaitForHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int32(2), attempts.Load())
}
