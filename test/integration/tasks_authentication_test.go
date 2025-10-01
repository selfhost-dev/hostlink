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
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"hostlink/app"
	"hostlink/config"
	"hostlink/domain/agent"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTasksEndpointAuthentication(t *testing.T) {
	t.Run("returns 401 when request has no authentication headers", func(t *testing.T) {
		env := setupTasksTestEnv(t)
		defer env.cleanup()

		req := httptest.NewRequest(http.MethodGet, "/api/v1/tasks", nil)
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("returns 200 and tasks when request has valid authentication", func(t *testing.T) {
		env := setupTasksTestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateKeyPair(t)

		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fingerprint",
		}

		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		agentID := testAgent.AID
		require.NotEmpty(t, agentID)

		publicKey, err := env.container.AgentRepository.GetPublicKeyByAgentID(context.Background(), agentID)
		require.NoError(t, err)
		assert.NotEmpty(t, publicKey)

		req := createAuthenticatedRequest(t, http.MethodGet, "/api/v1/tasks", agentID, privateKey)
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

type tasksTestEnv struct {
	db        *gorm.DB
	echo      *echo.Echo
	container *app.Container
}

func setupTasksTestEnv(t *testing.T) *tasksTestEnv {
	t.Helper()

	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	require.NoError(t, err)

	container := app.NewContainer(db)
	err = container.Migrate()
	require.NoError(t, err)

	e := echo.New()
	config.AddRoutesV2(e, container)

	return &tasksTestEnv{
		db:        db,
		echo:      e,
		container: container,
	}
}

func (env *tasksTestEnv) cleanup() {
	sqlDB, err := env.db.DB()
	if err == nil {
		sqlDB.Close()
	}
}

func generateKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyDER)
	return privateKey, publicKeyBase64
}

func createAuthenticatedRequest(t *testing.T, method, path, agentID string, privateKey *rsa.PrivateKey) *http.Request {
	t.Helper()

	timestamp := time.Now().Unix()
	nonce := generateTestNonce(t)

	message := fmt.Sprintf("%s|%d|%s", agentID, timestamp, nonce)
	hashed := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hashed[:], nil)
	require.NoError(t, err)

	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(signature))

	return req
}

func generateTestNonce(t *testing.T) string {
	t.Helper()
	bytes := make([]byte, 16)
	_, err := rand.Read(bytes)
	require.NoError(t, err)
	return base64.StdEncoding.EncodeToString(bytes)
}
