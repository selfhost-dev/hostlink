//go:build integration
// +build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hostlink/app"
	"hostlink/app/controller/tasks"
	"hostlink/config"
	"hostlink/domain/task"
	"hostlink/internal/validator"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTaskTestEnvironment(t *testing.T) (*echo.Echo, *app.Container) {
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to setup test DB: %v", err)
	}

	container := app.NewContainer(db)
	if err := container.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	e := echo.New()
	e.Validator = validator.New()
	config.AddRoutesV2(e, container)

	return e, container
}

func TestTaskCreation(t *testing.T) {
	t.Run("should create task successfully via v2 API", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

		reqBody := tasks.TaskRequest{
			Command:  "echo hello",
			Priority: 1,
		}
		body, _ := json.Marshal(reqBody)

		resp, err := http.Post(server.URL+"/api/v2/tasks", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var response task.Task
		json.NewDecoder(resp.Body).Decode(&response)
		assert.NotEmpty(t, response.ID)
		assert.Equal(t, "echo hello", response.Command)
		assert.Equal(t, 1, response.Priority)
		assert.Equal(t, "pending", response.Status)

		dbTask, err := container.TaskRepository.FindByID(nil, response.ID)
		require.NoError(t, err)
		assert.Equal(t, "echo hello", dbTask.Command)
	})

	t.Run("should reject task without command", func(t *testing.T) {
		e, _ := setupTaskTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

		reqBody := tasks.TaskRequest{
			Priority: 1,
		}
		body, _ := json.Marshal(reqBody)

		resp, err := http.Post(server.URL+"/api/v2/tasks", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("should reject task with invalid command syntax", func(t *testing.T) {
		e, _ := setupTaskTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

		reqBody := tasks.TaskRequest{
			Command:  "echo 'unclosed",
			Priority: 1,
		}
		body, _ := json.Marshal(reqBody)

		resp, err := http.Post(server.URL+"/api/v2/tasks", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("should create multiple tasks", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

		commands := []string{"ls -la", "pwd", "echo test"}
		var taskIDs []string

		for _, cmd := range commands {
			reqBody := tasks.TaskRequest{
				Command:  cmd,
				Priority: 1,
			}
			body, _ := json.Marshal(reqBody)

			resp, err := http.Post(server.URL+"/api/v2/tasks", "application/json", bytes.NewReader(body))
			require.NoError(t, err)

			var response task.Task
			json.NewDecoder(resp.Body).Decode(&response)
			taskIDs = append(taskIDs, response.ID)
			resp.Body.Close()
		}

		assert.Len(t, taskIDs, 3)

		allTasks, err := container.TaskRepository.FindAll(nil, task.TaskFilters{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(allTasks), 3)
	})
}
