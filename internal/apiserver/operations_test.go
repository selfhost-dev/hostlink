package apiserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"hostlink/app/services/requestsigner"
	"hostlink/internal/crypto"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestClient(t *testing.T, serverURL string) *client {
	t.Helper()

	tempDir := t.TempDir()
	keyPath := filepath.Join(tempDir, "test_key.pem")

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	cryptoService := crypto.NewService()
	err = cryptoService.SavePrivateKey(privateKey, keyPath)
	require.NoError(t, err)

	signer, err := requestsigner.New(keyPath, "test-agent-123")
	require.NoError(t, err)

	return &client{
		httpClient: http.DefaultClient,
		signer:     signer,
		baseURL:    serverURL,
		maxRetries: 0,
	}
}

// TestHeartbeat_Success - sends POST to /api/v1/agents/{agentID}/heartbeat with empty body
func TestHeartbeat_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	c := setupTestClient(t, server.URL)
	resp, err := c.Heartbeat(context.Background(), "test-agent-123")

	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

// TestHeartbeat_SendsToCorrectEndpoint - verifies correct URL path
func TestHeartbeat_SendsToCorrectEndpoint(t *testing.T) {
	var capturedPath string
	var capturedMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedPath = r.URL.Path
		capturedMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	c := setupTestClient(t, server.URL)
	resp, err := c.Heartbeat(context.Background(), "agent-xyz")

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "/api/v1/agents/agent-xyz/heartbeat", capturedPath)
	assert.Equal(t, "POST", capturedMethod)
}

// TestHeartbeat_SendsEmptyBody - verifies request body is empty
func TestHeartbeat_SendsEmptyBody(t *testing.T) {
	var bodySize int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodySize = r.ContentLength
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	c := setupTestClient(t, server.URL)
	resp, err := c.Heartbeat(context.Background(), "test-agent-123")

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, int64(0), bodySize)
}

// TestHeartbeat_AuthenticationHeadersIncluded - verifies signed request headers are present
func TestHeartbeat_AuthenticationHeadersIncluded(t *testing.T) {
	var headers http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":"ok"}`))
	}))
	defer server.Close()

	c := setupTestClient(t, server.URL)
	resp, err := c.Heartbeat(context.Background(), "test-agent-123")

	require.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, headers.Get("X-Agent-ID"))
	assert.NotEmpty(t, headers.Get("X-Timestamp"))
	assert.NotEmpty(t, headers.Get("X-Nonce"))
	assert.NotEmpty(t, headers.Get("X-Signature"))
}

// TestHeartbeat_ServerError - returns error on 5xx response
func TestHeartbeat_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	c := setupTestClient(t, server.URL)
	resp, err := c.Heartbeat(context.Background(), "test-agent-123")

	assert.Error(t, err)
	assert.Nil(t, resp)
}

// TestHeartbeat_Unauthorized - returns error on 401 response
func TestHeartbeat_Unauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte("unauthorized"))
	}))
	defer server.Close()

	c := setupTestClient(t, server.URL)
	resp, err := c.Heartbeat(context.Background(), "test-agent-123")

	assert.Error(t, err)
	assert.Nil(t, resp)
}

// TestHeartbeat_PendingTasks - decodes pending_tasks from response
func TestHeartbeat_PendingTasks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"message":"Heartbeat received","pending_tasks":[{"id":"tsk_test","command":"echo hello","status":"pending","priority":1}]}`))
	}))
	defer server.Close()

	c := setupTestClient(t, server.URL)
	resp, err := c.Heartbeat(context.Background(), "test-agent-123")

	require.NoError(t, err)
	require.NotNil(t, resp)
	assert.Len(t, resp.PendingTasks, 1)
	assert.Equal(t, "tsk_test", resp.PendingTasks[0].ID)
	assert.Equal(t, "echo hello", resp.PendingTasks[0].Command)
}
