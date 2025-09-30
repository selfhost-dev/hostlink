package taskfetcher

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"hostlink/app/services/agentstate"
	"hostlink/db/schema/taskschema"
	"hostlink/domain/nonce"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTaskFetcher_New(t *testing.T) {
	t.Run("should create task fetcher with request signer", func(t *testing.T) {
		tempDir := t.TempDir()
		keys := setupTestKeys(t)
		agentState := setupTestAgentState(t, "test-agent-123")
		privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

		fetcher, err := New(&Config{
			AgentState:      agentState,
			PrivateKeyPath:  privateKeyPath,
			ControlPlaneURL: "http://localhost:8080",
			ServerPublicKey: keys.publicKey,
			NonceRepo:       newMockNonceRepo(),
			Timeout:         10 * time.Second,
		})

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if fetcher == nil {
			t.Fatal("expected fetcher to be created")
		}
		if fetcher.signer == nil {
			t.Error("expected signer to be initialized")
		}
		if fetcher.verifier == nil {
			t.Error("expected verifier to be initialized")
		}
	})

	t.Run("should require agent state for agent ID", func(t *testing.T) {
		tempDir := t.TempDir()
		keys := setupTestKeys(t)
		privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

		agentState := agentstate.New(filepath.Join(tempDir, "agent.json"))
		agentState.AgentID = ""

		fetcher, err := New(&Config{
			AgentState:      agentState,
			PrivateKeyPath:  privateKeyPath,
			ControlPlaneURL: "http://localhost:8080",
			ServerPublicKey: keys.publicKey,
			NonceRepo:       newMockNonceRepo(),
		})

		if err == nil {
			t.Fatal("expected error for missing agent ID")
		}
		if fetcher != nil {
			t.Error("expected fetcher to be nil on error")
		}
	})

	t.Run("should configure HTTP client with timeout", func(t *testing.T) {
		tempDir := t.TempDir()
		keys := setupTestKeys(t)
		agentState := setupTestAgentState(t, "test-agent-123")
		privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

		customTimeout := 30 * time.Second
		fetcher, err := New(&Config{
			AgentState:      agentState,
			PrivateKeyPath:  privateKeyPath,
			ControlPlaneURL: "http://localhost:8080",
			ServerPublicKey: keys.publicKey,
			NonceRepo:       newMockNonceRepo(),
			Timeout:         customTimeout,
		})

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if fetcher.client.Timeout != customTimeout {
			t.Errorf("expected timeout %v, got %v", customTimeout, fetcher.client.Timeout)
		}
	})
}

func TestTaskFetcher_Fetch(t *testing.T) {
	t.Run("should add authentication headers to request", func(t *testing.T) {
		keys := setupTestKeys(t)
		var capturedRequest *http.Request

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedRequest = r

			serverID := "server-123"
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)
			nonce := generateTestNonce(t)

			message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, nonce)
			hashed := sha256.Sum256([]byte(message))
			signature, _ := rsa.SignPSS(rand.Reader, keys.privateKey, crypto.SHA256, hashed[:], nil)

			w.Header().Set("X-Server-ID", serverID)
			w.Header().Set("X-Timestamp", timestamp)
			w.Header().Set("X-Nonce", nonce)
			w.Header().Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]taskschema.Task{})
		}))
		defer server.Close()

		fetcher := setupTestFetcherWithKeys(t, server.URL, newMockNonceRepo(), keys)
		_, err := fetcher.Fetch()

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

	t.Run("should reject tasks with duplicate nonces", func(t *testing.T) {
		keys := setupTestKeys(t)
		fixedNonce := generateTestNonce(t)

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			serverID := "server-123"
			timestamp := strconv.FormatInt(time.Now().Unix(), 10)

			message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, fixedNonce)
			hashed := sha256.Sum256([]byte(message))
			signature, _ := rsa.SignPSS(rand.Reader, keys.privateKey, crypto.SHA256, hashed[:], nil)

			w.Header().Set("X-Server-ID", serverID)
			w.Header().Set("X-Timestamp", timestamp)
			w.Header().Set("X-Nonce", fixedNonce)
			w.Header().Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(createTestTasks(1))
		}))
		defer server.Close()

		nonceRepo := newMockNonceRepo()
		fetcher := setupTestFetcherWithKeys(t, server.URL, nonceRepo, keys)

		_, err := fetcher.Fetch()
		if err != nil {
			t.Fatalf("expected no error on first fetch, got %v", err)
		}

		_, err = fetcher.Fetch()
		if err == nil {
			t.Fatal("expected error for duplicate nonce")
		}
		if !strings.Contains(err.Error(), "duplicate nonce") {
			t.Errorf("expected duplicate nonce error, got: %v", err)
		}
	})

	t.Run("should store response nonces to prevent replay", func(t *testing.T) {
		keys := setupTestKeys(t)
		server := createSignedResponse(t, keys.privateKey, createTestTasks(2))
		defer server.Close()

		nonceRepo := newMockNonceRepo()
		fetcher := setupTestFetcherWithKeys(t, server.URL, nonceRepo, keys)

		_, err := fetcher.Fetch()

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(nonceRepo.nonces) != 1 {
			t.Errorf("expected 1 nonce stored, got %d", len(nonceRepo.nonces))
		}
	})

	t.Run("should parse tasks from JSON response", func(t *testing.T) {
		keys := setupTestKeys(t)
		expectedTasks := createTestTasks(3)
		server := createSignedResponse(t, keys.privateKey, expectedTasks)
		defer server.Close()

		fetcher := setupTestFetcherWithKeys(t, server.URL, newMockNonceRepo(), keys)
		tasks, err := fetcher.Fetch()

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(tasks) != 3 {
			t.Errorf("expected 3 tasks, got %d", len(tasks))
		}
		for i, task := range tasks {
			if task.Command != expectedTasks[i].Command {
				t.Errorf("task %d: expected command %s, got %s", i, expectedTasks[i].Command, task.Command)
			}
		}
	})

	t.Run("should handle empty task list", func(t *testing.T) {
		keys := setupTestKeys(t)
		server := createSignedResponse(t, keys.privateKey, []taskschema.Task{})
		defer server.Close()

		fetcher := setupTestFetcherWithKeys(t, server.URL, newMockNonceRepo(), keys)
		tasks, err := fetcher.Fetch()

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if len(tasks) != 0 {
			t.Errorf("expected 0 tasks, got %d", len(tasks))
		}
	})
}

func TestTaskFetcher_HandleErrors(t *testing.T) {
	t.Run("should handle network errors", func(t *testing.T) {
		fetcher := setupTestFetcher(t, "http://invalid-host-does-not-exist:9999", newMockNonceRepo())

		_, err := fetcher.Fetch()

		if err == nil {
			t.Fatal("expected error for network failure")
		}
		if !strings.Contains(err.Error(), "request failed") {
			t.Errorf("expected request failed error, got: %v", err)
		}
	})

	t.Run("should handle timeout errors", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(2 * time.Second)
		}))
		defer server.Close()

		tempDir := t.TempDir()
		keys := setupTestKeys(t)
		agentState := setupTestAgentState(t, "test-agent-123")
		privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

		fetcher, _ := New(&Config{
			AgentState:      agentState,
			PrivateKeyPath:  privateKeyPath,
			ControlPlaneURL: server.URL,
			ServerPublicKey: keys.publicKey,
			NonceRepo:       newMockNonceRepo(),
			Timeout:         100 * time.Millisecond,
		})

		_, err := fetcher.Fetch()

		if err == nil {
			t.Fatal("expected timeout error")
		}
		if !strings.Contains(err.Error(), "request failed") {
			t.Errorf("expected request failed error, got: %v", err)
		}
	})

	t.Run("should handle invalid JSON response", func(t *testing.T) {
		keys := setupTestKeys(t)
		server := createServerWithInvalidJSON(t, keys.privateKey)
		defer server.Close()

		fetcher := setupTestFetcherWithKeys(t, server.URL, newMockNonceRepo(), keys)
		_, err := fetcher.Fetch()

		if err == nil {
			t.Fatal("expected error for invalid JSON")
		}
		if !strings.Contains(err.Error(), "failed to decode response") {
			t.Errorf("expected decode error, got: %v", err)
		}
	})

	t.Run("should handle 500 server errors", func(t *testing.T) {
		server := createServerWithStatusCode(t, http.StatusInternalServerError)
		defer server.Close()

		fetcher := setupTestFetcher(t, server.URL, newMockNonceRepo())
		_, err := fetcher.Fetch()

		if err == nil {
			t.Fatal("expected error for 500 status code")
		}
		if !strings.Contains(err.Error(), "unexpected status code") {
			t.Errorf("expected status code error, got: %v", err)
		}
	})

	t.Run("should handle 403 forbidden errors", func(t *testing.T) {
		server := createServerWithStatusCode(t, http.StatusForbidden)
		defer server.Close()

		fetcher := setupTestFetcher(t, server.URL, newMockNonceRepo())
		_, err := fetcher.Fetch()

		if err == nil {
			t.Fatal("expected error for 403 status code")
		}
		if !strings.Contains(err.Error(), "unexpected status code: 403") {
			t.Errorf("expected 403 status code error, got: %v", err)
		}
	})
}

// Helper functions for tests
type testKeys struct {
	privateKey *rsa.PrivateKey
	publicKey  *rsa.PublicKey
}

type mockNonceRepo struct {
	nonces map[string]bool
	saveError bool
	existsError bool
}

func (m *mockNonceRepo) Save(ctx context.Context, n *nonce.Nonce) error {
	if m.saveError {
		return fmt.Errorf("mock save error")
	}
	m.nonces[n.Value] = true
	return nil
}

func (m *mockNonceRepo) Exists(ctx context.Context, value string) (bool, error) {
	if m.existsError {
		return false, fmt.Errorf("mock exists error")
	}
	return m.nonces[value], nil
}

func newMockNonceRepo() *mockNonceRepo {
	return &mockNonceRepo{
		nonces: make(map[string]bool),
	}
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

func saveTestPublicKey(t *testing.T, dir string, key *rsa.PublicKey) string {
	t.Helper()
	keyPath := filepath.Join(dir, "server-public.key")

	publicKeyBytes, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	publicKeyPEM := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}

	file, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("failed to create key file: %v", err)
	}
	defer file.Close()

	if err := pem.Encode(file, publicKeyPEM); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	return keyPath
}

func setupTestFetcherWithKeys(t *testing.T, serverURL string, nonceRepo NonceRepository, keys *testKeys) *taskfetcher {
	t.Helper()

	tempDir := t.TempDir()
	agentState := setupTestAgentState(t, "test-agent-123")
	privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

	fetcher, err := New(&Config{
		AgentState:      agentState,
		PrivateKeyPath:  privateKeyPath,
		ControlPlaneURL: serverURL,
		ServerPublicKey: keys.publicKey,
		NonceRepo:       nonceRepo,
		Timeout:         5 * time.Second,
	})

	if err != nil {
		t.Fatalf("failed to create test fetcher: %v", err)
	}

	return fetcher
}

func setupTestFetcher(t *testing.T, serverURL string, nonceRepo NonceRepository) *taskfetcher {
	t.Helper()
	keys := setupTestKeys(t)
	return setupTestFetcherWithKeys(t, serverURL, nonceRepo, keys)
}

func createSignedResponse(t *testing.T, privateKey *rsa.PrivateKey, tasks []taskschema.Task) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverID := "server-123"
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		nonce := generateTestNonce(t)

		message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, nonce)
		hashed := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hashed[:], nil)
		if err != nil {
			t.Fatalf("failed to sign response: %v", err)
		}

		w.Header().Set("X-Server-ID", serverID)
		w.Header().Set("X-Timestamp", timestamp)
		w.Header().Set("X-Nonce", nonce)
		w.Header().Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
		w.WriteHeader(http.StatusOK)

		json.NewEncoder(w).Encode(tasks)
	}))
}

func createServerWithStatusCode(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
	}))
}

func createServerWithInvalidJSON(t *testing.T, privateKey *rsa.PrivateKey) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverID := "server-123"
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		nonce := generateTestNonce(t)

		message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, nonce)
		hashed := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hashed[:], nil)
		if err != nil {
			t.Fatalf("failed to sign response: %v", err)
		}

		w.Header().Set("X-Server-ID", serverID)
		w.Header().Set("X-Timestamp", timestamp)
		w.Header().Set("X-Nonce", nonce)
		w.Header().Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
		w.WriteHeader(http.StatusOK)

		w.Write([]byte("invalid json"))
	}))
}

func generateTestNonce(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 16)
	_, err := rand.Read(bytes)
	if err != nil {
		t.Fatalf("failed to generate nonce: %v", err)
	}
	return base64.StdEncoding.EncodeToString(bytes)
}

func createTestTasks(count int) []taskschema.Task {
	tasks := make([]taskschema.Task, count)
	for i := range count {
		tasks[i] = taskschema.Task{
			PID:      fmt.Sprintf("tsk_%d", i+1),
			Command:  fmt.Sprintf("echo 'test %d'", i+1),
			Status:   "pending",
			Priority: i + 1,
		}
	}
	return tasks
}