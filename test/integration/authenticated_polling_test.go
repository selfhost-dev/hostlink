package integration

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
	"hostlink/app/services/taskfetcher"
	"hostlink/db/schema/taskschema"
	"hostlink/domain/nonce"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       newMockNonceRepo(env.db),
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		fetchedTasks, err := fetcher.Fetch()

		require.NoError(t, err)
		assert.Len(t, fetchedTasks, 3)
		assert.Equal(t, "echo 'integration test 1'", fetchedTasks[0].Command)
		assert.Contains(t, fetchedTasks[0].PID, "tsk_integration")
	})
}

func TestAuthenticatedPolling_SignatureVerification(t *testing.T) {
	t.Run("should verify valid server signatures", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		tasks := createTestTasks(2)
		env.createMockServer(t, tasks)

		tempDir := t.TempDir()
		savePrivateKey(t, filepath.Join(tempDir, "agent.key"), env.agentKeys)

		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  filepath.Join(tempDir, "agent.key"),
			ControlPlaneURL: env.server.URL,
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       newMockNonceRepo(env.db),
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		fetchedTasks, err := fetcher.Fetch()

		require.NoError(t, err)
		assert.Len(t, fetchedTasks, 2)
	})

	t.Run("should reject tasks with invalid signatures", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		env.createMockServerWithInvalidSignature(t)

		tempDir := t.TempDir()
		savePrivateKey(t, filepath.Join(tempDir, "agent.key"), env.agentKeys)

		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  filepath.Join(tempDir, "agent.key"),
			ControlPlaneURL: env.server.URL,
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       newMockNonceRepo(env.db),
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		_, err = fetcher.Fetch()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "verification failed")
	})

	t.Run("should reject tasks with tampered data", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		env.createMockServerWithTamperedData(t)

		tempDir := t.TempDir()
		savePrivateKey(t, filepath.Join(tempDir, "agent.key"), env.agentKeys)

		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  filepath.Join(tempDir, "agent.key"),
			ControlPlaneURL: env.server.URL,
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       newMockNonceRepo(env.db),
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		_, err = fetcher.Fetch()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "verification failed")
	})
}

func TestAuthenticatedPolling_ReplayAttackPrevention(t *testing.T) {
	t.Run("should prevent replay attacks with duplicate response nonces", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		fixedNonce := generateNonce(t)
		tasks := createTestTasks(1)
		env.createMockServerWithFixedNonce(t, fixedNonce, tasks)

		tempDir := t.TempDir()
		savePrivateKey(t, filepath.Join(tempDir, "agent.key"), env.agentKeys)

		nonceRepo := newMockNonceRepo(env.db)
		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  filepath.Join(tempDir, "agent.key"),
			ControlPlaneURL: env.server.URL,
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       nonceRepo,
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		_, err = fetcher.Fetch()
		require.NoError(t, err)

		_, err = fetcher.Fetch()

		require.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate nonce")
	})

	t.Run("should store response nonces to database", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		tasks := createTestTasks(2)
		env.createMockServer(t, tasks)

		tempDir := t.TempDir()
		savePrivateKey(t, filepath.Join(tempDir, "agent.key"), env.agentKeys)

		nonceRepo := newMockNonceRepo(env.db)
		fetcher, err := taskfetcher.New(&taskfetcher.Config{
			AgentState:      env.agentState,
			PrivateKeyPath:  filepath.Join(tempDir, "agent.key"),
			ControlPlaneURL: env.server.URL,
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       nonceRepo,
			Timeout:         5 * time.Second,
		})
		require.NoError(t, err)

		_, err = fetcher.Fetch()
		require.NoError(t, err)

		var count int64
		err = env.db.Model(&nonce.Nonce{}).Count(&count).Error
		require.NoError(t, err)
		assert.Equal(t, int64(1), count)
	})

	t.Run("should clean up expired nonces", func(t *testing.T) {
		env := setupPollingTestEnv(t)
		defer env.cleanup()

		oldNonce := &nonce.Nonce{
			Value:     "old-nonce-1",
			CreatedAt: time.Now().Add(-10 * time.Minute),
		}
		recentNonce := &nonce.Nonce{
			Value:     "recent-nonce-1",
			CreatedAt: time.Now(),
		}

		require.NoError(t, env.db.Create(oldNonce).Error)
		require.NoError(t, env.db.Create(recentNonce).Error)

		cutoffTime := time.Now().Add(-6 * time.Minute)
		result := env.db.Where("created_at < ?", cutoffTime).Delete(&nonce.Nonce{})

		require.NoError(t, result.Error)
		assert.Equal(t, int64(1), result.RowsAffected)

		var remainingCount int64
		env.db.Model(&nonce.Nonce{}).Count(&remainingCount)
		assert.Equal(t, int64(1), remainingCount)
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
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       newMockNonceRepo(env.db),
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
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       newMockNonceRepo(env.db),
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
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       newMockNonceRepo(env.db),
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
			ServerPublicKey: &env.serverKeys.PublicKey,
			NonceRepo:       newMockNonceRepo(env.db),
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
	serverKeys     *rsa.PrivateKey
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

	err = db.AutoMigrate(&nonce.Nonce{})
	require.NoError(t, err)

	serverKeys, err := rsa.GenerateKey(rand.Reader, 2048)
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
	savePublicKey(t, filepath.Join(tempDir, "server-public.key"), &serverKeys.PublicKey)

	env := &pollingTestEnv{
		db:             db,
		serverKeys:     serverKeys,
		agentKeys:      agentKeys,
		agentState:     agentState,
		stateDir:       stateDir,
		requestHeaders: make(map[string]string),
	}

	return env
}

func (env *pollingTestEnv) createMockServer(t *testing.T, tasks []taskschema.Task) {
	t.Helper()

	env.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range r.Header {
			if len(v) > 0 {
				env.requestHeaders[k] = v[0]
			}
		}

		serverID := "server-integration"
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		nonceBytes := make([]byte, 16)
		rand.Read(nonceBytes)
		nonce := base64.StdEncoding.EncodeToString(nonceBytes)

		message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, nonce)
		hashed := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPSS(rand.Reader, env.serverKeys, crypto.SHA256, hashed[:], nil)
		require.NoError(t, err)

		w.Header().Set("X-Server-ID", serverID)
		w.Header().Set("X-Timestamp", timestamp)
		w.Header().Set("X-Nonce", nonce)
		w.Header().Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
		w.WriteHeader(http.StatusOK)

		json.NewEncoder(w).Encode(tasks)
	}))
}

func (env *pollingTestEnv) createMockServerWithInvalidSignature(t *testing.T) {
	t.Helper()

	env.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Server-ID", "server-123")
		w.Header().Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		w.Header().Set("X-Nonce", "invalid-nonce")
		w.Header().Set("X-Signature", "invalid-signature")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]taskschema.Task{})
	}))
}

func (env *pollingTestEnv) createMockServerWithFixedNonce(t *testing.T, fixedNonce string, tasks []taskschema.Task) {
	t.Helper()

	env.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverID := "server-integration"
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)

		message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, fixedNonce)
		hashed := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPSS(rand.Reader, env.serverKeys, crypto.SHA256, hashed[:], nil)
		require.NoError(t, err)

		w.Header().Set("X-Server-ID", serverID)
		w.Header().Set("X-Timestamp", timestamp)
		w.Header().Set("X-Nonce", fixedNonce)
		w.Header().Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
		w.WriteHeader(http.StatusOK)

		json.NewEncoder(w).Encode(tasks)
	}))
}

func (env *pollingTestEnv) createMockServerWithTamperedData(t *testing.T) {
	t.Helper()

	env.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverID := "server-original"
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		nonce := generateNonce(t)

		message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, nonce)
		hashed := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPSS(rand.Reader, env.serverKeys, crypto.SHA256, hashed[:], nil)
		require.NoError(t, err)

		w.Header().Set("X-Server-ID", "server-tampered")
		w.Header().Set("X-Timestamp", timestamp)
		w.Header().Set("X-Nonce", nonce)
		w.Header().Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]taskschema.Task{})
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

func savePublicKey(t *testing.T, path string, key *rsa.PublicKey) {
	t.Helper()

	publicKeyBytes, err := x509.MarshalPKIXPublicKey(key)
	require.NoError(t, err)

	publicKeyPEM := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}

	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()

	err = pem.Encode(file, publicKeyPEM)
	require.NoError(t, err)
}

func generateNonce(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 16)
	_, err := rand.Read(bytes)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(bytes)
}

func createTestTasks(count int) []taskschema.Task {
	tasks := make([]taskschema.Task, count)
	for i := range count {
		tasks[i] = taskschema.Task{
			PID:      fmt.Sprintf("tsk_integration_%d", i+1),
			Command:  fmt.Sprintf("echo 'integration test %d'", i+1),
			Status:   "pending",
			Priority: i + 1,
		}
	}
	return tasks
}

type mockNonceRepo struct {
	db *gorm.DB
}

func newMockNonceRepo(db *gorm.DB) *mockNonceRepo {
	return &mockNonceRepo{db: db}
}

func (r *mockNonceRepo) Save(ctx context.Context, n *nonce.Nonce) error {
	return r.db.Create(n).Error
}

func (r *mockNonceRepo) Exists(ctx context.Context, value string) (bool, error) {
	var count int64
	err := r.db.Model(&nonce.Nonce{}).Where("value = ?", value).Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}
