//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"hostlink/app"
	"hostlink/app/controller/tasks"
	"hostlink/config"
	"hostlink/domain/agent"
	"hostlink/domain/task"
	"hostlink/internal/validator"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTaskUpdate_Authentication(t *testing.T) {
	t.Run("authenticated request succeeds", func(t *testing.T) {
		env := setupTaskUpdateTestEnv(t)
		defer env.cleanup()

		testTask := &task.Task{
			Command:  "echo hello",
			Status:   "pending",
			Priority: 1,
		}
		err := env.container.TaskRepository.Create(context.Background(), testTask)
		require.NoError(t, err)

		privateKey, publicKeyBase64 := generateTestKeyPair(t)
		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-update",
		}
		err = env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		updateReq := tasks.TaskUpdateRequest{
			Status:   "completed",
			Output:   "hello",
			ExitCode: 0,
		}
		body, _ := json.Marshal(updateReq)

		req := createSignedRequestWithBody(t, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%s", testTask.ID), testAgent.ID, privateKey, time.Now(), bytes.NewReader(body))
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("unauthenticated request fails (401)", func(t *testing.T) {
		env := setupTaskUpdateTestEnv(t)
		defer env.cleanup()

		testTask := &task.Task{
			Command:  "echo hello",
			Status:   "pending",
			Priority: 1,
		}
		err := env.container.TaskRepository.Create(context.Background(), testTask)
		require.NoError(t, err)

		updateReq := tasks.TaskUpdateRequest{
			Status:   "completed",
			Output:   "hello",
			ExitCode: 0,
		}
		body, _ := json.Marshal(updateReq)

		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/tasks/%s", testTask.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusUnauthorized, rec.Code)
	})
}

func TestTaskUpdate_Success(t *testing.T) {
	t.Run("agent updates task with output successfully", func(t *testing.T) {
		env := setupTaskUpdateTestEnv(t)
		defer env.cleanup()

		testTask := &task.Task{
			Command:  "ls -la",
			Status:   "pending",
			Priority: 1,
		}
		err := env.container.TaskRepository.Create(context.Background(), testTask)
		require.NoError(t, err)

		privateKey, publicKeyBase64 := generateTestKeyPair(t)
		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-output",
		}
		err = env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		updateReq := tasks.TaskUpdateRequest{
			Status:   "completed",
			Output:   "total 48\ndrwxr-xr-x",
			ExitCode: 0,
		}
		body, _ := json.Marshal(updateReq)

		req := createSignedRequestWithBody(t, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%s", testTask.ID), testAgent.ID, privateKey, time.Now(), bytes.NewReader(body))
		rec := httptest.NewRecorder()
		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		updatedTask, err := env.container.TaskRepository.FindByID(context.Background(), testTask.ID)
		require.NoError(t, err)
		assert.Equal(t, "completed", updatedTask.Status)
		assert.Equal(t, "total 48\ndrwxr-xr-x", updatedTask.Output)
		assert.Equal(t, 0, updatedTask.ExitCode)
	})

	t.Run("agent updates task with exit code 0", func(t *testing.T) {
		env := setupTaskUpdateTestEnv(t)
		defer env.cleanup()

		testTask := &task.Task{
			Command:  "pwd",
			Status:   "running",
			Priority: 1,
		}
		err := env.container.TaskRepository.Create(context.Background(), testTask)
		require.NoError(t, err)

		privateKey, publicKeyBase64 := generateTestKeyPair(t)
		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-exit0",
		}
		err = env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		updateReq := tasks.TaskUpdateRequest{
			Status:   "completed",
			Output:   "/home/user",
			ExitCode: 0,
		}
		body, _ := json.Marshal(updateReq)

		req := createSignedRequestWithBody(t, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%s", testTask.ID), testAgent.ID, privateKey, time.Now(), bytes.NewReader(body))
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		updatedTask, err := env.container.TaskRepository.FindByID(context.Background(), testTask.ID)
		require.NoError(t, err)
		assert.Equal(t, 0, updatedTask.ExitCode)
		assert.Equal(t, "completed", updatedTask.Status)
	})

	t.Run("agent updates task with non-zero exit code", func(t *testing.T) {
		env := setupTaskUpdateTestEnv(t)
		defer env.cleanup()

		testTask := &task.Task{
			Command:  "false",
			Status:   "running",
			Priority: 1,
		}
		err := env.container.TaskRepository.Create(context.Background(), testTask)
		require.NoError(t, err)

		privateKey, publicKeyBase64 := generateTestKeyPair(t)
		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-exit1",
		}
		err = env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		updateReq := tasks.TaskUpdateRequest{
			Status:   "failed",
			Output:   "",
			ExitCode: 1,
		}
		body, _ := json.Marshal(updateReq)

		req := createSignedRequestWithBody(t, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%s", testTask.ID), testAgent.ID, privateKey, time.Now(), bytes.NewReader(body))
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		updatedTask, err := env.container.TaskRepository.FindByID(context.Background(), testTask.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, updatedTask.ExitCode)
		assert.Equal(t, "failed", updatedTask.Status)
	})

	t.Run("agent updates task with error message", func(t *testing.T) {
		env := setupTaskUpdateTestEnv(t)
		defer env.cleanup()

		testTask := &task.Task{
			Command:  "invalid-command",
			Status:   "running",
			Priority: 1,
		}
		err := env.container.TaskRepository.Create(context.Background(), testTask)
		require.NoError(t, err)

		privateKey, publicKeyBase64 := generateTestKeyPair(t)
		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-error",
		}
		err = env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		updateReq := tasks.TaskUpdateRequest{
			Status:   "failed",
			Error:    "command not found",
			ExitCode: 127,
		}
		body, _ := json.Marshal(updateReq)

		req := createSignedRequestWithBody(t, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%s", testTask.ID), testAgent.ID, privateKey, time.Now(), bytes.NewReader(body))
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		updatedTask, err := env.container.TaskRepository.FindByID(context.Background(), testTask.ID)
		require.NoError(t, err)
		assert.Equal(t, "command not found", updatedTask.Error)
		assert.Equal(t, "failed", updatedTask.Status)
		assert.Equal(t, 127, updatedTask.ExitCode)
	})
}

func TestTaskUpdate_ErrorCases(t *testing.T) {
	t.Run("update non-existent task returns 404", func(t *testing.T) {
		env := setupTaskUpdateTestEnv(t)
		defer env.cleanup()

		privateKey, publicKeyBase64 := generateTestKeyPair(t)
		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-404",
		}
		err := env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		updateReq := tasks.TaskUpdateRequest{
			Status:   "completed",
			Output:   "test",
			ExitCode: 0,
		}
		body, _ := json.Marshal(updateReq)

		req := createSignedRequestWithBody(t, http.MethodPut, "/api/v1/tasks/nonexistent-task-id", testAgent.ID, privateKey, time.Now(), bytes.NewReader(body))
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("update with invalid data returns 400", func(t *testing.T) {
		env := setupTaskUpdateTestEnv(t)
		defer env.cleanup()

		testTask := &task.Task{
			Command:  "echo test",
			Status:   "pending",
			Priority: 1,
		}
		err := env.container.TaskRepository.Create(context.Background(), testTask)
		require.NoError(t, err)

		privateKey, publicKeyBase64 := generateTestKeyPair(t)
		testAgent := &agent.Agent{
			PublicKey:     publicKeyBase64,
			PublicKeyType: "rsa",
			Fingerprint:   "test-fp-400",
		}
		err = env.container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		invalidReq := map[string]interface{}{
			"output": "missing status field",
		}
		body, _ := json.Marshal(invalidReq)

		req := createSignedRequestWithBody(t, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%s", testTask.ID), testAgent.ID, privateKey, time.Now(), bytes.NewReader(body))
		rec := httptest.NewRecorder()

		env.echo.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

type taskUpdateTestEnv struct {
	db        *gorm.DB
	echo      *echo.Echo
	container *app.Container
}

func setupTaskUpdateTestEnv(t *testing.T) *taskUpdateTestEnv {
	t.Helper()

	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	require.NoError(t, err)

	container := app.NewContainer(db)
	err = container.Migrate()
	require.NoError(t, err)

	e := echo.New()
	e.Validator = validator.New()
	config.AddRoutesV2(e, container)

	return &taskUpdateTestEnv{
		db:        db,
		echo:      e,
		container: container,
	}
}

func (env *taskUpdateTestEnv) cleanup() {
	sqlDB, err := env.db.DB()
	if err == nil {
		sqlDB.Close()
	}
}

func createSignedRequestWithBody(t *testing.T, method, path, agentID string, privateKey *rsa.PrivateKey, timestamp time.Time, body io.Reader) *http.Request {
	t.Helper()

	req := createSignedRequest(t, method, path, agentID, privateKey, timestamp, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}
