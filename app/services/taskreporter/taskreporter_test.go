package taskreporter

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"hostlink/app/services/agentstate"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTaskReporter_New(t *testing.T) {
	t.Run("should create service with request signer", func(t *testing.T) {
		tempDir := t.TempDir()
		keys := setupTestKeys(t)
		agentState := setupTestAgentState(t, "test-agent-123")
		privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

		reporter, err := New(&Config{
			AgentState:      agentState,
			PrivateKeyPath:  privateKeyPath,
			ControlPlaneURL: "http://localhost:8080",
			Timeout:         10 * time.Second,
		})

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if reporter == nil {
			t.Fatal("expected reporter to be created")
		}
		if reporter.signer == nil {
			t.Error("expected signer to be initialized")
		}
	})

	t.Run("should require agent state for agent ID", func(t *testing.T) {
		tempDir := t.TempDir()
		keys := setupTestKeys(t)
		privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

		agentState := agentstate.New(filepath.Join(tempDir, "agent.json"))
		agentState.AgentID = ""

		reporter, err := New(&Config{
			AgentState:      agentState,
			PrivateKeyPath:  privateKeyPath,
			ControlPlaneURL: "http://localhost:8080",
		})

		if err == nil {
			t.Fatal("expected error for missing agent ID")
		}
		if reporter != nil {
			t.Error("expected reporter to be nil on error")
		}
	})

	t.Run("should configure HTTP client with timeout", func(t *testing.T) {
		tempDir := t.TempDir()
		keys := setupTestKeys(t)
		agentState := setupTestAgentState(t, "test-agent-123")
		privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

		customTimeout := 30 * time.Second
		reporter, err := New(&Config{
			AgentState:      agentState,
			PrivateKeyPath:  privateKeyPath,
			ControlPlaneURL: "http://localhost:8080",
			Timeout:         customTimeout,
		})

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if reporter.client.Timeout != customTimeout {
			t.Errorf("expected timeout %v, got %v", customTimeout, reporter.client.Timeout)
		}
	})

	t.Run("should use default timeout when not specified", func(t *testing.T) {
		tempDir := t.TempDir()
		keys := setupTestKeys(t)
		agentState := setupTestAgentState(t, "test-agent-123")
		privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

		reporter, err := New(&Config{
			AgentState:      agentState,
			PrivateKeyPath:  privateKeyPath,
			ControlPlaneURL: "http://localhost:8080",
			Timeout:         0,
		})

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		expectedTimeout := 10 * time.Second
		if reporter.client.Timeout != expectedTimeout {
			t.Errorf("expected default timeout %v, got %v", expectedTimeout, reporter.client.Timeout)
		}
	})
}

type testKeys struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

func setupTestKeys(t *testing.T) *testKeys {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test keys: %v", err)
	}
	return &testKeys{
		privateKey: privateKey,
		publicKey:  &privateKey.PublicKey,
	}
}

func setupTestAgentState(t *testing.T, agentID string) *agentstate.AgentState {
	t.Helper()
	tempDir := t.TempDir()
	statePath := filepath.Join(tempDir, "agent.json")

	state := agentstate.New(statePath)
	state.AgentID = agentID
	state.LastSyncTime = time.Now().Unix()

	if err := state.Save(); err != nil {
		t.Fatalf("failed to save agent state: %v", err)
	}

	return state
}

func saveTestPrivateKey(t *testing.T, dir string, key *rsa.PrivateKey) string {
	t.Helper()
	keyPath := filepath.Join(dir, "agent.key")

	privateKeyBytes := x509.MarshalPKCS1PrivateKey(key)
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}

	file, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("failed to create key file: %v", err)
	}
	defer file.Close()

	if err := pem.Encode(file, privateKeyPEM); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	return keyPath
}

func TestTaskReporter_Report(t *testing.T) {
	t.Run("should send PUT request to correct endpoint", func(t *testing.T) {
		keys := setupTestKeys(t)
		var capturedRequest *http.Request

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedRequest = r
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		reporter := setupTestReporter(t, server.URL, keys)
		taskID := "task-123"
		result := &TaskResult{
			Status:   "completed",
			Output:   "test output",
			Error:    "",
			ExitCode: 0,
		}

		err := reporter.Report(taskID, result)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if capturedRequest.Method != "PUT" {
			t.Errorf("expected PUT method, got %s", capturedRequest.Method)
		}
		expectedPath := "/api/v1/tasks/" + taskID
		if capturedRequest.URL.Path != expectedPath {
			t.Errorf("expected path %s, got %s", expectedPath, capturedRequest.URL.Path)
		}
	})

	t.Run("should add authentication headers", func(t *testing.T) {
		keys := setupTestKeys(t)
		var capturedRequest *http.Request

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedRequest = r
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		reporter := setupTestReporter(t, server.URL, keys)
		result := &TaskResult{Status: "completed", Output: "test", Error: "", ExitCode: 0}

		err := reporter.Report("task-123", result)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		requiredHeaders := []string{"X-Agent-ID", "X-Timestamp", "X-Nonce", "X-Signature"}
		for _, header := range requiredHeaders {
			if capturedRequest.Header.Get(header) == "" {
				t.Errorf("expected header %s to be set", header)
			}
		}
	})

	t.Run("should marshal TaskResult to JSON", func(t *testing.T) {
		keys := setupTestKeys(t)
		var capturedBody []byte

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			capturedBody = body
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		reporter := setupTestReporter(t, server.URL, keys)
		result := &TaskResult{
			Status:   "completed",
			Output:   "test output",
			Error:    "test error",
			ExitCode: 1,
		}

		err := reporter.Report("task-123", result)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		var decodedResult TaskResult
		if err := json.Unmarshal(capturedBody, &decodedResult); err != nil {
			t.Fatalf("failed to decode JSON: %v", err)
		}
		if decodedResult.Status != result.Status {
			t.Errorf("expected status %s, got %s", result.Status, decodedResult.Status)
		}
		if decodedResult.Output != result.Output {
			t.Errorf("expected output %s, got %s", result.Output, decodedResult.Output)
		}
		if decodedResult.Error != result.Error {
			t.Errorf("expected error %s, got %s", result.Error, decodedResult.Error)
		}
		if decodedResult.ExitCode != result.ExitCode {
			t.Errorf("expected exit_code %d, got %d", result.ExitCode, decodedResult.ExitCode)
		}
	})

	t.Run("should handle 200 response", func(t *testing.T) {
		keys := setupTestKeys(t)
		server := createServerWithStatusCode(t, http.StatusOK)
		defer server.Close()

		reporter := setupTestReporter(t, server.URL, keys)
		result := &TaskResult{Status: "completed", Output: "test", Error: "", ExitCode: 0}

		err := reporter.Report("task-123", result)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("should handle 404 response", func(t *testing.T) {
		keys := setupTestKeys(t)
		server := createServerWithStatusCode(t, http.StatusNotFound)
		defer server.Close()

		reporter := setupTestReporter(t, server.URL, keys)
		result := &TaskResult{Status: "completed", Output: "test", Error: "", ExitCode: 0}

		err := reporter.Report("task-123", result)

		if err == nil {
			t.Fatal("expected error for 404 status code")
		}
		if !strings.Contains(err.Error(), "unexpected status code: 404") {
			t.Errorf("expected 404 status code error, got: %v", err)
		}
	})

	t.Run("should handle 500 response", func(t *testing.T) {
		keys := setupTestKeys(t)
		server := createServerWithStatusCode(t, http.StatusInternalServerError)
		defer server.Close()

		reporter := setupTestReporter(t, server.URL, keys)
		result := &TaskResult{Status: "completed", Output: "test", Error: "", ExitCode: 0}

		err := reporter.Report("task-123", result)

		if err == nil {
			t.Fatal("expected error for 500 status code")
		}
		if !strings.Contains(err.Error(), "unexpected status code: 500") {
			t.Errorf("expected 500 status code error, got: %v", err)
		}
	})

	t.Run("should handle network errors", func(t *testing.T) {
		reporter := setupTestReporter(t, "http://invalid-host-does-not-exist:9999", setupTestKeys(t))
		result := &TaskResult{Status: "completed", Output: "test", Error: "", ExitCode: 0}

		err := reporter.Report("task-123", result)

		if err == nil {
			t.Fatal("expected error for network failure")
		}
		if !strings.Contains(err.Error(), "request failed") {
			t.Errorf("expected request failed error, got: %v", err)
		}
	})
}

func setupTestReporter(t *testing.T, serverURL string, keys *testKeys) *taskreporter {
	t.Helper()

	tempDir := t.TempDir()
	agentState := setupTestAgentState(t, "test-agent-123")
	privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

	reporter, err := New(&Config{
		AgentState:      agentState,
		PrivateKeyPath:  privateKeyPath,
		ControlPlaneURL: serverURL,
		Timeout:         5 * time.Second,
	})

	if err != nil {
		t.Fatalf("failed to create test reporter: %v", err)
	}

	return reporter
}

func createServerWithStatusCode(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
	}))
}
