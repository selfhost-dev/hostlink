package httpclient

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"hostlink/version"
)

func TestAgentTransport_SetsAllHeaders(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// Verify X-Agent-Version header
	if got := receivedHeaders.Get("X-Agent-Version"); got != version.Version {
		t.Errorf("X-Agent-Version = %q, want %q", got, version.Version)
	}

	// Verify X-Agent-OS header
	if got := receivedHeaders.Get("X-Agent-OS"); got != runtime.GOOS {
		t.Errorf("X-Agent-OS = %q, want %q", got, runtime.GOOS)
	}

	// Verify X-Agent-Arch header
	if got := receivedHeaders.Get("X-Agent-Arch"); got != runtime.GOARCH {
		t.Errorf("X-Agent-Arch = %q, want %q", got, runtime.GOARCH)
	}
}

func TestAgentTransport_PreservesExistingHeaders(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-Custom-Header", "custom-value")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// Verify custom header is preserved
	if got := receivedHeaders.Get("X-Custom-Header"); got != "custom-value" {
		t.Errorf("X-Custom-Header = %q, want %q", got, "custom-value")
	}

	// Verify agent headers are still set
	if got := receivedHeaders.Get("X-Agent-Version"); got != version.Version {
		t.Errorf("X-Agent-Version = %q, want %q", got, version.Version)
	}
}

func TestAgentTransport_DoesNotMutateOriginalRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(5 * time.Second)
	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	// Store original header count
	originalHeaderCount := len(req.Header)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()

	// Verify original request was not mutated
	if len(req.Header) != originalHeaderCount {
		t.Errorf("original request was mutated: header count changed from %d to %d", originalHeaderCount, len(req.Header))
	}
}

func TestAgentTransport_UsesDefaultTransportWhenBaseIsNil(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create transport with nil Base
	transport := &AgentTransport{Base: nil}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}

	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("unexpected error with nil Base: %v", err)
	}
	resp.Body.Close()
}

func TestNewClient_SetsTimeout(t *testing.T) {
	client := NewClient(42 * time.Second)
	if client.Timeout != 42*time.Second {
		t.Errorf("Timeout = %v, want %v", client.Timeout, 42*time.Second)
	}
}
