//go:build integration
// +build integration

package integration

import (
	"bytes"
	"encoding/json"
	"hostlink/app/controller/tasks"
	"hostlink/domain/task"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskDetails_GetExistingTask(t *testing.T) {
	e, container := setupTaskTestEnvironment(t)
	server := httptest.NewServer(e)
	defer server.Close()

	reqBody := tasks.TaskRequest{
		Command:  "ls -la",
		Priority: 1,
	}
	body, _ := json.Marshal(reqBody)

	createResp, err := http.Post(server.URL+"/api/v2/tasks", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer createResp.Body.Close()

	var createdTask task.Task
	json.NewDecoder(createResp.Body).Decode(&createdTask)

	getResp, err := http.Get(server.URL + "/api/v2/tasks/" + createdTask.ID)
	require.NoError(t, err)
	defer getResp.Body.Close()

	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var response task.Task
	json.NewDecoder(getResp.Body).Decode(&response)
	assert.Equal(t, createdTask.ID, response.ID)
	assert.Equal(t, "ls -la", response.Command)
	assert.Equal(t, "pending", response.Status)
	assert.Equal(t, 1, response.Priority)

	dbTask, err := container.TaskRepository.FindByID(nil, createdTask.ID)
	require.NoError(t, err)
	assert.Equal(t, createdTask.ID, dbTask.ID)
}

func TestTaskDetails_GetNonExistentTask(t *testing.T) {
	e, _ := setupTaskTestEnvironment(t)
	server := httptest.NewServer(e)
	defer server.Close()

	getResp, err := http.Get(server.URL + "/api/v2/tasks/nonexistent_id")
	require.NoError(t, err)
	defer getResp.Body.Close()

	assert.Equal(t, http.StatusNotFound, getResp.StatusCode)
}

func TestTaskDetails_GetTaskWithOutput(t *testing.T) {
	e, container := setupTaskTestEnvironment(t)
	server := httptest.NewServer(e)
	defer server.Close()

	reqBody := tasks.TaskRequest{
		Command:  "echo hello",
		Priority: 1,
	}
	body, _ := json.Marshal(reqBody)

	createResp, err := http.Post(server.URL+"/api/v2/tasks", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer createResp.Body.Close()

	var createdTask task.Task
	json.NewDecoder(createResp.Body).Decode(&createdTask)

	dbTask, err := container.TaskRepository.FindByID(nil, createdTask.ID)
	require.NoError(t, err)
	dbTask.Status = "completed"
	dbTask.Output = "hello\n"
	dbTask.ExitCode = 0
	err = container.TaskRepository.Update(nil, dbTask)
	require.NoError(t, err)

	getResp, err := http.Get(server.URL + "/api/v2/tasks/" + createdTask.ID)
	require.NoError(t, err)
	defer getResp.Body.Close()

	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var response task.Task
	json.NewDecoder(getResp.Body).Decode(&response)
	assert.Equal(t, createdTask.ID, response.ID)
	assert.Equal(t, "completed", response.Status)
	assert.Equal(t, "hello\n", response.Output)
	assert.Equal(t, 0, response.ExitCode)
}

func TestTaskDetails_GetTaskWithoutOutput(t *testing.T) {
	e, _ := setupTaskTestEnvironment(t)
	server := httptest.NewServer(e)
	defer server.Close()

	reqBody := tasks.TaskRequest{
		Command:  "sleep 100",
		Priority: 1,
	}
	body, _ := json.Marshal(reqBody)

	createResp, err := http.Post(server.URL+"/api/v2/tasks", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer createResp.Body.Close()

	var createdTask task.Task
	json.NewDecoder(createResp.Body).Decode(&createdTask)

	getResp, err := http.Get(server.URL + "/api/v2/tasks/" + createdTask.ID)
	require.NoError(t, err)
	defer getResp.Body.Close()

	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var response task.Task
	json.NewDecoder(getResp.Body).Decode(&response)
	assert.Equal(t, createdTask.ID, response.ID)
	assert.Equal(t, "pending", response.Status)
	assert.Empty(t, response.Output)
}
