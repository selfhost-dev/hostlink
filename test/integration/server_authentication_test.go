//go:build integration
// +build integration

package integration

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"hostlink/app"
	"hostlink/config"
	"hostlink/domain/agent"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestServerAuthentication_UnauthenticatedRequests(t *testing.T) {
	t.Run("should return 401 for request with no authentication headers", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestServerAuthentication_MissingHeaders(t *testing.T) {
	t.Run("should return 401 when X-Agent-ID header is missing", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		req.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		req.Header.Set("X-Nonce", generateNonce(t))
		req.Header.Set("X-Signature", "some-signature")
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("should return 401 when X-Timestamp header is missing", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		req.Header.Set("X-Agent-ID", "agent-123")
		req.Header.Set("X-Nonce", generateNonce(t))
		req.Header.Set("X-Signature", "some-signature")
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("should return 401 when X-Nonce header is missing", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		req.Header.Set("X-Agent-ID", "agent-123")
		req.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		req.Header.Set("X-Signature", "some-signature")
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("should return 401 when X-Signature header is missing", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		req.Header.Set("X-Agent-ID", "agent-123")
		req.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		req.Header.Set("X-Nonce", generateNonce(t))
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestServerAuthentication_InvalidSignature(t *testing.T) {
	t.Run("should return 401 for request with invalid signature", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		_, publicKeyBase64 := generateTestKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-invalid-sig",
		}

		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		agentID := testAgent.ID
		timestamp := time.Now().Unix()
		nonce := generateNonce(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		req.Header.Set("X-Agent-ID", agentID)
		req.Header.Set("X-Timestamp", strconv.FormatInt(timestamp, 10))
		req.Header.Set("X-Nonce", nonce)
		req.Header.Set("X-Signature", "invalid-signature-base64")
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("should return 401 when signature is signed with wrong key", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		_, publicKeyBase64 := generateTestKeyPair(t)
		wrongPrivateKey, _ := generateTestKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-wrong-key",
		}

		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		agentID := testAgent.ID
		timestamp := time.Now().Unix()
		nonce := generateNonce(t)

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

func TestServerAuthentication_ValidSignature(t *testing.T) {
	t.Run("should return 200 for request with valid signature", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateTestKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-valid",
		}

		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		req := createSignedRequest(t, http.MethodGet, "/api/v1/tasks", testAgent.ID, privateKey, time.Now())
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestServerAuthentication_TimestampValidation(t *testing.T) {
	t.Run("should return 401 for request with expired timestamp (>5 minutes old)", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateTestKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-expired",
		}

		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		oldTimestamp := time.Now().Add(-6 * time.Minute)
		req := createSignedRequest(t, http.MethodGet, "/api/v1/tasks", testAgent.ID, privateKey, oldTimestamp)
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("should return 401 for request with future timestamp (>5 minutes ahead)", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateTestKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-future",
		}

		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		futureTimestamp := time.Now().Add(6 * time.Minute)
		req := createSignedRequest(t, http.MethodGet, "/api/v1/tasks", testAgent.ID, privateKey, futureTimestamp)
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("should return 200 for request with timestamp within 5 minute window", func(t *testing.T) {
		env := setupServerAuthTestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateTestKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-valid-window",
		}

		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		recentTimestamp := time.Now().Add(-4 * time.Minute)
		req := createSignedRequest(t, http.MethodGet, "/api/v1/tasks", testAgent.ID, privateKey, recentTimestamp)
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

type serverAuthTestEnv struct {
	db        *gorm.DB
	echo      *echo.Echo
	container *app.Container
}

func setupServerAuthTestEnv(t *testing.T) *serverAuthTestEnv {
	t.Helper()

	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	require.NoError(t, err)

	container := app.NewContainer(db)
	err = container.Migrate()
	require.NoError(t, err)

	e := echo.New()
	config.AddRoutesV2(e, container)

	return &serverAuthTestEnv{
		db:        db,
		echo:      e,
		container: container,
	}
}

func (env *serverAuthTestEnv) cleanup() {
	sqlDB, err := env.db.DB()
	if err == nil {
		sqlDB.Close()
	}
}

func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyDER)
	return privateKey, publicKeyBase64
}

func createSignedRequest(t *testing.T, method, path, agentID string, privateKey *rsa.PrivateKey, timestamp time.Time) *http.Request {
	t.Helper()

	timestampUnix := timestamp.Unix()
	nonce := generateNonce(t)

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

func generateNonce(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 16)
	_, err := rand.Read(bytes)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(bytes)
}
