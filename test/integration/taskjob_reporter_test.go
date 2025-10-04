//go:build integration
// +build integration

package integration

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"hostlink/app"
	"hostlink/app/jobs/taskjob"
	"hostlink/app/services/agentstate"
	"hostlink/app/services/taskfetcher"
	"hostlink/app/services/taskreporter"
	"hostlink/config"
	"hostlink/domain/agent"
	"hostlink/domain/task"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTaskJobReporter_SendsUpdateViaAPI(t *testing.T) {
	env := setupTaskJobTestEnv(t)
	defer env.cleanup()

	testTask := &task.Task{
		Command:  "echo hello",
		Status:   "pending",
		Priority: 1,
	}
	err := env.container.TaskRepository.Create(context.Background(), testTask)
	require.NoError(t, err)

	var updateReceived bool
	var mu sync.Mutex

	env.echo.PUT("/api/v1/tasks/:id", func(c echo.Context) error {
		mu.Lock()
		updateReceived = true
		mu.Unlock()
		return c.NoContent(http.StatusOK)
	})

	taskjob.Register(env.fetcher, env.reporter)

	time.Sleep(3 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, updateReceived, "Expected task update to be sent via API")

	updatedTask, err := env.container.TaskRepository.FindByID(context.Background(), testTask.ID)
	require.NoError(t, err)
	assert.Equal(t, "pending", updatedTask.Status, "Task should not be updated in DB directly by taskjob")
}

func TestTaskJobReporter_CapturesTaskOutput(t *testing.T) {
	env := setupTaskJobTestEnv(t)
	defer env.cleanup()

	testTask := &task.Task{
		Command:  "echo 'test output'",
		Status:   "pending",
		Priority: 1,
	}
	err := env.container.TaskRepository.Create(context.Background(), testTask)
	require.NoError(t, err)

	var receivedOutput string
	var mu sync.Mutex

	env.echo.PUT("/api/v1/tasks/:id", func(c echo.Context) error {
		var req map[string]any
		if err := c.Bind(&req); err != nil {
			return err
		}
		mu.Lock()
		if output, ok := req["output"].(string); ok {
			receivedOutput = output
		}
		mu.Unlock()
		return c.NoContent(http.StatusOK)
	})

	taskjob.Register(env.fetcher, env.reporter)

	time.Sleep(3 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	assert.Contains(t, receivedOutput, "test output", "Task output should be captured and sent")
}

func TestTaskJobReporter_SendsExitCode(t *testing.T) {
	env := setupTaskJobTestEnv(t)
	defer env.cleanup()

	testTask := &task.Task{
		Command:  "exit 42",
		Status:   "pending",
		Priority: 1,
	}
	err := env.container.TaskRepository.Create(context.Background(), testTask)
	require.NoError(t, err)

	var receivedExitCode int
	var mu sync.Mutex

	env.echo.PUT("/api/v1/tasks/:id", func(c echo.Context) error {
		var req map[string]any
		if err := c.Bind(&req); err != nil {
			return err
		}
		mu.Lock()
		if exitCode, ok := req["exit_code"].(float64); ok {
			receivedExitCode = int(exitCode)
		}
		mu.Unlock()
		return c.NoContent(http.StatusOK)
	})

	taskjob.Register(env.fetcher, env.reporter)

	time.Sleep(3 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 42, receivedExitCode, "Exit code should be sent correctly")
}

func TestTaskJobReporter_SendsErrorMessages(t *testing.T) {
	env := setupTaskJobTestEnv(t)
	defer env.cleanup()

	testTask := &task.Task{
		Command:  "invalid-command-xyz",
		Status:   "pending",
		Priority: 1,
	}
	err := env.container.TaskRepository.Create(context.Background(), testTask)
	require.NoError(t, err)

	var receivedError string
	var receivedStatus string
	var mu sync.Mutex

	env.echo.PUT("/api/v1/tasks/:id", func(c echo.Context) error {
		var req map[string]any
		if err := c.Bind(&req); err != nil {
			return err
		}
		mu.Lock()
		if errMsg, ok := req["error"].(string); ok {
			receivedError = errMsg
		}
		if status, ok := req["status"].(string); ok {
			receivedStatus = status
		}
		mu.Unlock()
		return c.NoContent(http.StatusOK)
	})

	taskjob.Register(env.fetcher, env.reporter)

	time.Sleep(3 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	assert.NotEmpty(t, receivedError, "Error message should be sent")
	assert.Equal(t, "completed", receivedStatus, "Status should be completed even with error")
}

func TestTaskJobReporter_IncludesAuthHeaders(t *testing.T) {
	env := setupTaskJobTestEnv(t)
	defer env.cleanup()

	testTask := &task.Task{
		Command:  "echo test",
		Status:   "pending",
		Priority: 1,
	}
	err := env.container.TaskRepository.Create(context.Background(), testTask)
	require.NoError(t, err)

	var hasAgentID, hasTimestamp, hasNonce, hasSignature bool
	var mu sync.Mutex

	env.echo.PUT("/api/v1/tasks/:id", func(c echo.Context) error {
		mu.Lock()
		hasAgentID = c.Request().Header.Get("X-Agent-ID") != ""
		hasTimestamp = c.Request().Header.Get("X-Timestamp") != ""
		hasNonce = c.Request().Header.Get("X-Nonce") != ""
		hasSignature = c.Request().Header.Get("X-Signature") != ""
		mu.Unlock()
		return c.NoContent(http.StatusOK)
	})

	taskjob.Register(env.fetcher, env.reporter)

	time.Sleep(3 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, hasAgentID, "X-Agent-ID header should be present")
	assert.True(t, hasTimestamp, "X-Timestamp header should be present")
	assert.True(t, hasNonce, "X-Nonce header should be present")
	assert.True(t, hasSignature, "X-Signature header should be present")
}

func TestTaskJobReporter_FailedUpdateIsLogged(t *testing.T) {
	env := setupTaskJobTestEnv(t)
	defer env.cleanup()

	testTask := &task.Task{
		Command:  "echo test",
		Status:   "pending",
		Priority: 1,
	}
	err := env.container.TaskRepository.Create(context.Background(), testTask)
	require.NoError(t, err)

	var updateAttempted bool
	var mu sync.Mutex

	env.echo.PUT("/api/v1/tasks/:id", func(c echo.Context) error {
		mu.Lock()
		updateAttempted = true
		mu.Unlock()
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "server error"})
	})

	taskjob.Register(env.fetcher, env.reporter)

	time.Sleep(3 * time.Second)

	mu.Lock()
	defer mu.Unlock()
	assert.True(t, updateAttempted, "Update should be attempted even if it fails")
}

type taskJobTestEnv struct {
	db         *gorm.DB
	echo       *echo.Echo
	container  *app.Container
	server     *httptest.Server
	tempDir    string
	agentState *agentstate.AgentState
	fetcher    taskfetcher.TaskFetcher
	reporter   taskreporter.TaskReporter
}

func setupTaskJobTestEnv(t *testing.T) *taskJobTestEnv {
	t.Helper()

	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	require.NoError(t, err)

	container := app.NewContainer(db)
	err = container.Migrate()
	require.NoError(t, err)

	e := echo.New()
	config.AddRoutesV2(e, container)

	server := httptest.NewServer(e)

	tempDir := t.TempDir()

	os.Setenv("HOSTLINK_STATE_PATH", tempDir)
	os.Setenv("HOSTLINK_PRIVATE_KEY_PATH", tempDir+"/private_key.pem")
	os.Setenv("HOSTLINK_CONTROL_PLANE_URL", server.URL)

	agentState := agentstate.New(tempDir)

	privateKey, publicKeyBase64 := generateTestKeyPair(t)
	testAgent := &agent.Agent{
		PublicKey:     publicKeyBase64,
		PublicKeyType: "rsa",
		Fingerprint:   "test-taskjob-fp-" + t.Name(),
	}
	err = container.AgentRepository.Create(context.Background(), testAgent)
	require.NoError(t, err)

	err = agentState.SetAgentID(testAgent.ID)
	require.NoError(t, err)

	keyPath := tempDir + "/private_key.pem"
	err = savePrivateKeyToPEM(privateKey, keyPath)
	require.NoError(t, err)

	fetcher, err := taskfetcher.New(&taskfetcher.Config{
		AgentState:      agentState,
		PrivateKeyPath:  keyPath,
		ControlPlaneURL: server.URL,
	})
	require.NoError(t, err)

	reporter, err := taskreporter.New(&taskreporter.Config{
		AgentState:      agentState,
		PrivateKeyPath:  keyPath,
		ControlPlaneURL: server.URL,
	})
	require.NoError(t, err)

	return &taskJobTestEnv{
		db:         db,
		echo:       e,
		container:  container,
		server:     server,
		tempDir:    tempDir,
		agentState: agentState,
		fetcher:    fetcher,
		reporter:   reporter,
	}
}

func (env *taskJobTestEnv) cleanup() {
	sqlDB, err := env.db.DB()
	if err == nil {
		sqlDB.Close()
	}
	if env.server != nil {
		env.server.Close()
	}
	os.Unsetenv("HOSTLINK_STATE_PATH")
	os.Unsetenv("HOSTLINK_PRIVATE_KEY_PATH")
	os.Unsetenv("HOSTLINK_CONTROL_PLANE_URL")
}

func savePrivateKeyToPEM(key *rsa.PrivateKey, path string) error {
	keyBytes := x509.MarshalPKCS1PrivateKey(key)
	keyPEM := "-----BEGIN RSA PRIVATE KEY-----\n"
	keyPEM += base64.StdEncoding.EncodeToString(keyBytes)
	keyPEM += "\n-----END RSA PRIVATE KEY-----\n"
	return os.WriteFile(path, []byte(keyPEM), 0600)
}
