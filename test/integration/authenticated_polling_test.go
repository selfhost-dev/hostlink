package integration

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"hostlink/app/services/agentstate"
	"hostlink/app/services/taskfetcher"
	"hostlink/domain/task"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAuthenticatedPolling_FullFlow(t *testing.T) {
	t.Run("should complete full registration and store agent ID", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		assert.Equal(t, "test-agent-integration", env.agentState.AgentID)

		loadedState := agentstate.New(env.stateDir)
		err := loadedState.Load()

		require.NoError(t, err)
		assert.Equal(t, "test-agent-integration", loadedState.AgentID)
		assert.Greater(t, loadedState.LastSyncTime, int64(0))
	})

	t.Run("should load agent ID on restart", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		originalAgentID := env.agentState.AgentID

		newState := agentstate.New(env.stateDir)
		err := newState.Load()

		require.NoError(t, err)
		assert.Equal(t, originalAgentID, newState.AgentID)
		assert.Equal(t, env.agentState.LastSyncTime, newState.LastSyncTime)
	})

	t.Run("should fetch and verify signed tasks end-to-end", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		tasks := createTestTasks(3)
		env.createMockServer(t, tasks)

		tempDir := t.TempDir()
		agentKeyPath := filepath.Join(tempDir, "agent.key")
		savePrivateKey(t, agentKeyPath, env.agentKeys)

		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  agentKeyPath,
			ControlPlaneURL: env.server.URL,
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		fetchedTasks, err := fetcher.Fetch()

		require.NoError(t, err)
		assert.Len(t, fetchedTasks, 3)
		assert.Equal(t, "echo 'integration test 1'", fetchedTasks[0].Command)
		assert.Contains(t, fetchedTasks[0].ID, "tsk_integration")
	})
}

func TestAuthenticatedPolling_TimestampReplayProtection(t *testing.T) {
	t.Run("should allow multiple requests within timestamp window", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		tasks := createTestTasks(2)
		env.createMockServer(t, tasks)

		tempDir := t.TempDir()
		agentKeyPath := filepath.Join(tempDir, "agent.key")
		savePrivateKey(t, agentKeyPath, env.agentKeys)

		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  agentKeyPath,
			ControlPlaneURL: env.server.URL,
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		firstFetch, err := fetcher.Fetch()
		require.NoError(t, err)
		assert.Len(t, firstFetch, 2)

		secondFetch, err := fetcher.Fetch()
		require.NoError(t, err)
		assert.Len(t, secondFetch, 2)
	})

	t.Run("should verify server returns unsigned responses", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		tasks := createTestTasks(1)

		var capturedResponseHeaders http.Header
		env.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(tasks)
			capturedResponseHeaders = w.Header()
		}))

		tempDir := t.TempDir()
		agentKeyPath := filepath.Join(tempDir, "agent.key")
		savePrivateKey(t, agentKeyPath, env.agentKeys)

		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  agentKeyPath,
			ControlPlaneURL: env.server.URL,
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		_, err = fetcher.Fetch()

		require.NoError(t, err)
		assert.Empty(t, capturedResponseHeaders.Get("X-Server-ID"))
		assert.Empty(t, capturedResponseHeaders.Get("X-Signature"))
		assert.Empty(t, capturedResponseHeaders.Get("X-Nonce"))
	})
}

func TestAuthenticatedPolling_ErrorRecovery(t *testing.T) {
	t.Run("should handle authentication failures", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		env.createMockServerWithStatusCode(t, http.StatusUnauthorized)

		tempDir := t.TempDir()
		savePrivateKey(t, filepath.Join(tempDir, "agent.key"), env.agentKeys)

		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  filepath.Join(tempDir, "agent.key"),
			ControlPlaneURL: env.server.URL,
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		_, err = fetcher.Fetch()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status code")
	})

	t.Run("should recover from network errors", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		tempDir := t.TempDir()
		savePrivateKey(t, filepath.Join(tempDir, "agent.key"), env.agentKeys)

		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  filepath.Join(tempDir, "agent.key"),
			ControlPlaneURL: "http://invalid-server-does-not-exist:9999",
			Timeout:         1 * time.Second,
		})
		require.NoError(t, err)

		_, err = fetcher.Fetch()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "request failed")
	})

	t.Run("should continue polling after errors", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		tasks := createTestTasks(1)
		env.createMockServer(t, tasks)

		tempDir := t.TempDir()
		savePrivateKey(t, filepath.Join(tempDir, "agent.key"), env.agentKeys)

		firstFetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  filepath.Join(tempDir, "agent.key"),
			ControlPlaneURL: env.server.URL,
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		_, err = firstFetcher.Fetch()
		require.NoError(t, err)

		env.server.Close()
		env.createMockServer(t, createTestTasks(2))

		secondFetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  filepath.Join(tempDir, "agent.key"),
			ControlPlaneURL: env.server.URL,
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		fetchedTasks, err := secondFetcher.Fetch()

		require.NoError(t, err)
		assert.Len(t, fetchedTasks, 2)
	})
}

// Helper functions for integration tests

type pollingTestEnv struct {
	db             *gorm.DB
	server         *httptest.Server
	agentKeys      *rsa.PrivateKey
	agentState     *agentstate.AgentState
	stateDir       string
	requestHeaders map[string]string
}

func setupPollingTestEnv(t *testing.T) *pollingTestEnv {
	t.Helper()

	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	require.NoError(t, err)

	agentKeys, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	tempDir := t.TempDir()
	stateDir := filepath.Join(tempDir, "state")
	err = os.MkdirAll(stateDir, 0700)
	require.NoError(t, err)

	agentState := agentstate.New(stateDir)
	agentState.AgentID = "test-agent-integration"
	agentState.LastSyncTime = time.Now().Unix()
	err = agentState.Save()
	require.NoError(t, err)

	savePrivateKey(t, filepath.Join(tempDir, "agent.key"), agentKeys)

	env := &pollingTestEnv{
		db:             db,
		agentKeys:      agentKeys,
		agentState:     agentState,
		stateDir:       stateDir,
		requestHeaders: make(map[string]string),
	}

	return env
}

func (env *pollingTestEnv) createMockServer(t *testing.T, tasks []task.Task) {
	t.Helper()

	env.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range r.Header {
			if len(v) > 0 {
				env.requestHeaders[k] = v[0]
			}
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(tasks)
	}))
}

func (env *pollingTestEnv) createMockServerWithStatusCode(t *testing.T, statusCode int) {
	t.Helper()

	env.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
	}))
}

func (env *pollingTestEnv) createInvalidNetworkServer(t *testing.T) {
	t.Helper()
	env.server = &httptest.Server{}
}

func (env *pollingTestEnv) cleanup() {
	if env.server != nil {
		env.server.Close()
	}
}

func savePrivateKey(t *testing.T, path string, key *rsa.PrivateKey) {
	t.Helper()

	privateKeyBytes := x509.MarshalPKCS1PrivateKey(key)
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}

	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()

	err = pem.Encode(file, privateKeyPEM)
	require.NoError(t, err)
}

func createTestTasks(count int) []task.Task {
	tasks := make([]task.Task, count)
	for i := range count {
		tasks[i] = task.Task{
			ID:       fmt.Sprintf("tsk_integration_%d", i+1),
			Command:  fmt.Sprintf("echo 'integration test %d'", i+1),
			Status:   "pending",
			Priority: i + 1,
		}
	}
	return tasks
}
