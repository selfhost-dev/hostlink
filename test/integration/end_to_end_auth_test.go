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
	"fmt"
	"hostlink/app"
	"hostlink/config"
	"hostlink/domain/agent"
	"hostlink/domain/task"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEndToEndAuth_CompleteFlow(t *testing.T) {
	t.Run("should complete full flow: registration → polling → task creation", func(t *testing.T) {
		env := setupE2ETestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateE2EKeyPair(t)
		fingerprint := "e2e-test-fp-001"

		req := createRegistrationRequest(t, fingerprint, publicKeyBase64)
		rec := httptest.NewRecorder()
		env.echo.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Logf("Registration failed with status %d, body: %s", rec.Code, rec.Body.String())
		}
		require.Equal(t, http.StatusOK, rec.Code)

		var regResponse map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &regResponse)
		require.NoError(t, err)
		agentID := regResponse["id"].(string)
		require.NotEmpty(t, agentID)

		newTask := &task.Task{
			Command:  "echo 'e2e test'",
			Priority: 1,
		}
		err = env.container.TaskRepository.Create(context.Background(), newTask)
		require.NoError(t, err)

		pollReq := createSignedE2ERequest(t, http.MethodGet, "/api/v1/tasks", agentID, privateKey, time.Now())
		pollRec := httptest.NewRecorder()
		env.echo.ServeHTTP(pollRec, pollReq)

		require.Equal(t, http.StatusOK, pollRec.Code)

		var tasks []map[string]interface{}
		err = json.Unmarshal(pollRec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.Len(t, tasks, 1)
		assert.Equal(t, "echo 'e2e test'", tasks[0]["Command"])
	})
}

func TestEndToEndAuth_SignAndVerify(t *testing.T) {
	t.Run("should sign request on agent side and verify on server side", func(t *testing.T) {
		env := setupE2ETestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateE2EKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "e2e-sign-verify",
		}
		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		agentID := testAgent.ID
		timestamp := time.Now().Unix()
		nonce := generateE2ENonce(t)

		message := fmt.Sprintf("%s|%d|%s", agentID, timestamp, nonce)
		hashed := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hashed[:], nil)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Timestamp", strconv.FormatInt(timestamp, 10))
		req.Header.Set("X-Nonce", nonce)
		req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("should reject request with invalid signature", func(t *testing.T) {
		env := setupE2ETestEnv(t)
		defer env.cleanup()

		_, publicKeyBase64 := generateE2EKeyPair(t)
		wrongPrivateKey, _ := generateE2EKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "e2e-invalid-sig",
		}
		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		agentID := testAgent.ID
		timestamp := time.Now().Unix()
		nonce := generateE2ENonce(t)

		message := fmt.Sprintf("%s|%d|%s", agentID, timestamp, nonce)
		hashed := sha256.Sum256([]byte(message))
		signature, err := rsa.SignPSS(rand.Reader, wrongPrivateKey, crypto.SHA256, hashed[:], nil)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Timestamp", strconv.FormatInt(timestamp, 10))
		req.Header.Set("X-Nonce", nonce)
		req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestEndToEndAuth_MultipleRequestsWithDifferentTimestamps(t *testing.T) {
	t.Run("should accept multiple sequential requests with different timestamps", func(t *testing.T) {
		env := setupE2ETestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateE2EKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "e2e-multi-timestamps",
		}
		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		agentID := testAgent.ID

		for i := range 5 {
			timestamp := time.Now().Add(time.Duration(i) * time.Second)
			req := createSignedE2ERequest(t, http.MethodGet, "/api/v1/tasks", agentID, privateKey, timestamp)
			rec := httptest.NewRecorder()

			env.echo.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code, "Request %d should succeed", i+1)
		}
	})

	t.Run("should handle requests with timestamps spread across time window", func(t *testing.T) {
		env := setupE2ETestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateE2EKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "e2e-time-window",
		}
		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		agentID := testAgent.ID

		timestamps := []time.Time{
			time.Now().Add(-4 * time.Minute),
			time.Now().Add(-2 * time.Minute),
			time.Now(),
			time.Now().Add(2 * time.Minute),
			time.Now().Add(4 * time.Minute),
		}

		for i, ts := range timestamps {
			req := createSignedE2ERequest(t, http.MethodGet, "/api/v1/tasks", agentID, privateKey, ts)
			rec := httptest.NewRecorder()

			env.echo.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusOK, rec.Code, "Request %d with timestamp %v should succeed", i+1, ts)
		}
	})
}

func TestEndToEndAuth_ConcurrentAuthenticatedRequests(t *testing.T) {
	t.Run("should handle concurrent requests from multiple agents", func(t *testing.T) {
		env := setupE2ETestEnv(t)
		defer env.cleanup()

		numAgents := 10
		agents := make([]*testAgentInfo, numAgents)

		for i := range numAgents {
			privateKey, publicKeyBase64 := generateE2EKeyPair(t)

			testAgent := &agent.Agent{
				PublicKey:     publicKeyBase64,
				PublicKeyType: "rsa",
				Fingerprint:   fmt.Sprintf("e2e-concurrent-%d", i),
			}
			err := env.container.AgentRepository.Create(context.Background(), testAgent)
			require.NoError(t, err)

			agents[i] = &testAgentInfo{
				agentID:    testAgent.ID,
				privateKey: privateKey,
			}
		}

		var wg sync.WaitGroup
		results := make([]int, numAgents)

		for i := range numAgents {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()

				agent := agents[index]
				req := createSignedE2ERequest(t, http.MethodGet, "/api/v1/tasks", agent.agentID, agent.privateKey, time.Now())
				rec := httptest.NewRecorder()

				env.echo.ServeHTTP(rec, req)
				results[index] = rec.Code
			}(i)
		}

		wg.Wait()

		for i, code := range results {
			assert.Equal(t, http.StatusOK, code, "Agent %d request should succeed", i)
		}
	})

	t.Run("should handle concurrent requests from same agent", func(t *testing.T) {
		env := setupE2ETestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateE2EKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "e2e-same-agent-concurrent",
		}
		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		agentID := testAgent.ID
		numRequests := 20

		var wg sync.WaitGroup
		results := make([]int, numRequests)

		for i := range numRequests {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()

				req := createSignedE2ERequest(t, http.MethodGet, "/api/v1/tasks", agentID, privateKey, time.Now())
				rec := httptest.NewRecorder()

				env.echo.ServeHTTP(rec, req)
				results[index] = rec.Code
			}(i)
		}

		wg.Wait()

		successCount := 0
		for _, code := range results {
			if code == http.StatusOK {
				successCount++
			}
		}

		assert.Equal(t, numRequests, successCount, "All concurrent requests should succeed")
	})
}

type e2eTestEnv struct {
	db        *gorm.DB
	echo      *echo.Echo
	container *app.Container
}

type testAgentInfo struct {
	agentID    string
	privateKey *rsa.PrivateKey
}

func setupE2ETestEnv(t *testing.T) *e2eTestEnv {
	t.Helper()

	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	require.NoError(t, err)

	container := app.NewContainer(db)
	err = container.Migrate()
	require.NoError(t, err)

	e := echo.New()
	e.Validator = &e2eValidator{}
	config.AddRoutesV2(e, container)

	return &e2eTestEnv{
		db:        db,
		echo:      e,
		container: container,
	}
}

type e2eValidator struct{}

func (v *e2eValidator) Validate(i interface{}) error {
	return nil
}

func (env *e2eTestEnv) cleanup() {
	sqlDB, err := env.db.DB()
	if err == nil {
		sqlDB.Close()
	}
}

func generateE2EKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyDER)
	return privateKey, publicKeyBase64
}

func createSignedE2ERequest(t *testing.T, method, path, agentID string, privateKey *rsa.PrivateKey, timestamp time.Time) *http.Request {
	t.Helper()

	timestampUnix := timestamp.Unix()
	nonce := generateE2ENonce(t)

	message := fmt.Sprintf("%s|%d|%s", agentID, timestampUnix, nonce)
	hashed := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hashed[:], nil)
	require.NoError(t, err)

	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Timestamp", strconv.FormatInt(timestampUnix, 10))
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(signature))

	return req
}

func createRegistrationRequest(t *testing.T, fingerprint, publicKey string) *http.Request {
	t.Helper()

	body := fmt.Sprintf(`{"fingerprint":"%s","token_id":"test-token-id","token_key":"test-token-key","public_key":"%s","public_key_type":"rsa"}`, fingerprint, publicKey)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	return req
}

func generateE2ENonce(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 16)
	_, err := rand.Read(bytes)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(bytes)
}
