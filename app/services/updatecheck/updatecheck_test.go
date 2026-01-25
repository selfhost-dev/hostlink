package updatecheck

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"hostlink/app/services/requestsigner"
)

func TestCheck_UpdateAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc123",
			AgentSize:       52428800,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	checker := newTestChecker(t, server.Client(), server.URL, nil)
	info, err := checker.Check("1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !info.UpdateAvailable {
		t.Error("expected UpdateAvailable to be true")
	}
	if info.TargetVersion != "2.0.0" {
		t.Errorf("expected TargetVersion 2.0.0, got %s", info.TargetVersion)
	}
	if info.AgentURL != "https://example.com/agent.tar.gz" {
		t.Errorf("expected AgentURL https://example.com/agent.tar.gz, got %s", info.AgentURL)
	}
	if info.AgentSHA256 != "abc123" {
		t.Errorf("expected AgentSHA256 abc123, got %s", info.AgentSHA256)
	}
	if info.AgentSize != 52428800 {
		t.Errorf("expected AgentSize 52428800, got %d", info.AgentSize)
	}
}

func TestCheck_NoUpdateAvailable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := UpdateInfo{
			UpdateAvailable: false,
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	checker := newTestChecker(t, server.Client(), server.URL, nil)
	info, err := checker.Check("2.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.UpdateAvailable {
		t.Error("expected UpdateAvailable to be false")
	}
}

func TestCheck_NetworkError(t *testing.T) {
	checker := newTestChecker(t, http.DefaultClient, "http://localhost:1", nil)
	_, err := checker.Check("1.0.0")
	if err == nil {
		t.Fatal("expected error for bad URL, got nil")
	}
}

func TestCheck_SignsRequest(t *testing.T) {
	var receivedHeaders http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		resp := UpdateInfo{UpdateAvailable: false}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	signer := &mockSigner{agentID: "agent-123"}
	checker := newTestChecker(t, server.Client(), server.URL, signer)
	_, err := checker.Check("1.0.0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedHeaders.Get("X-Agent-ID") != "agent-123" {
		t.Errorf("expected X-Agent-ID header agent-123, got %s", receivedHeaders.Get("X-Agent-ID"))
	}
	if receivedHeaders.Get("X-Timestamp") == "" {
		t.Error("expected X-Timestamp header to be set")
	}
	if receivedHeaders.Get("X-Nonce") == "" {
		t.Error("expected X-Nonce header to be set")
	}
	if receivedHeaders.Get("X-Signature") == "" {
		t.Error("expected X-Signature header to be set")
	}
}

func TestCheck_SendsCurrentVersionAsHeader(t *testing.T) {
	var receivedVersion string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedVersion = r.Header.Get("X-Agent-Version")
		resp := UpdateInfo{UpdateAvailable: false}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	checker := newTestChecker(t, server.Client(), server.URL, nil)
	_, err := checker.Check("1.5.3")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if receivedVersion != "1.5.3" {
		t.Errorf("expected X-Agent-Version header 1.5.3, got %s", receivedVersion)
	}
}

func TestCheck_NoQueryParams(t *testing.T) {
	var receivedRawQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRawQuery = r.URL.RawQuery
		resp := UpdateInfo{UpdateAvailable: false}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	checker := newTestChecker(t, server.Client(), server.URL, nil)
	checker.Check("1.5.3")

	if receivedRawQuery != "" {
		t.Errorf("expected no query params, got %s", receivedRawQuery)
	}
}

func TestCheck_HTTPErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	checker := newTestChecker(t, server.Client(), server.URL, nil)
	_, err := checker.Check("1.0.0")
	if err == nil {
		t.Fatal("expected error for 500 status, got nil")
	}
}

func TestCheck_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json"))
	}))
	defer server.Close()

	checker := newTestChecker(t, server.Client(), server.URL, nil)
	_, err := checker.Check("1.0.0")
	if err == nil {
		t.Fatal("expected error for invalid JSON, got nil")
	}
}

func TestCheck_UsesGETMethod(t *testing.T) {
	var receivedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		resp := UpdateInfo{UpdateAvailable: false}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	checker := newTestChecker(t, server.Client(), server.URL, nil)
	checker.Check("1.0.0")

	if receivedMethod != http.MethodGet {
		t.Errorf("expected GET method, got %s", receivedMethod)
	}
}

func TestCheck_UsesCorrectPath(t *testing.T) {
	var receivedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		resp := UpdateInfo{UpdateAvailable: false}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	checker, err := New(server.Client(), server.URL, "agent-123", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	checker.Check("1.0.0")

	if receivedPath != "/api/v1/agents/agent-123/update" {
		t.Errorf("expected path /api/v1/agents/agent-123/update, got %s", receivedPath)
	}
}

func TestNew_ReturnsErrorForEmptyAgentID(t *testing.T) {
	_, err := New(http.DefaultClient, "http://example.com", "", nil)
	if err == nil {
		t.Fatal("expected error for empty agentID, got nil")
	}
}

// newTestChecker is a test helper that creates an UpdateChecker with a default agent ID.
func newTestChecker(t *testing.T, client *http.Client, url string, signer RequestSignerInterface) *UpdateChecker {
	t.Helper()
	checker, err := New(client, url, "agent-123", signer)
	if err != nil {
		t.Fatalf("failed to create UpdateChecker: %v", err)
	}
	return checker
}

// mockSigner implements the RequestSigner interface for testing
type mockSigner struct {
	agentID string
}

func (m *mockSigner) SignRequest(req *http.Request) error {
	req.Header.Set("X-Agent-ID", m.agentID)
	req.Header.Set("X-Timestamp", "1234567890")
	req.Header.Set("X-Nonce", "testnonce")
	req.Header.Set("X-Signature", "testsignature")
	return nil
}

// Ensure mockSigner satisfies the interface
var _ RequestSignerInterface = (*mockSigner)(nil)
var _ RequestSignerInterface = (*requestsigner.RequestSigner)(nil)
