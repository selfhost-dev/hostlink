//go:build smoke
// +build smoke

package smoke

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const baseURL = "http://localhost:8080"

type TaskRequest struct {
	Command  string `json:"command"`
	Priority int    `json:"priority"`
}

type Task struct {
	ID          string `json:"ID"`
	Command     string `json:"Command"`
	Status      string `json:"Status"`
	Priority    int    `json:"Priority"`
	Output      string `json:"Output"`
	ExitCode    int    `json:"ExitCode"`
	CreatedAt   string `json:"CreatedAt"`
	StartedAt   string `json:"StartedAt"`
	CompletedAt string `json:"CompletedAt"`
}

type ErrorResponse struct {
	Error string `json:"error"`
}

func TestTaskDetailsSmoke_GetExistingTask(t *testing.T) {
	reqBody := TaskRequest{
		Command:  "echo 'smoke test'",
		Priority: 1,
	}
	body, _ := json.Marshal(reqBody)

	createResp, err := http.Post(baseURL+"/api/v2/tasks", "application/json", bytes.NewReader(body))
	require.NoError(t, err, "Failed to create task - is the server running?")
	defer createResp.Body.Close()

	require.Equal(t, http.StatusCreated, createResp.StatusCode)

	var createdTask Task
	json.NewDecoder(createResp.Body).Decode(&createdTask)
	require.NotEmpty(t, createdTask.ID, "Created task should have an ID")

	fmt.Printf("Created task with ID: %s\n", createdTask.ID)

	getResp, err := http.Get(baseURL + "/api/v2/tasks/" + createdTask.ID)
	require.NoError(t, err)
	defer getResp.Body.Close()

	assert.Equal(t, http.StatusOK, getResp.StatusCode)

	var response Task
	json.NewDecoder(getResp.Body).Decode(&response)
	assert.Equal(t, createdTask.ID, response.ID)
	assert.Equal(t, "echo 'smoke test'", response.Command)
	assert.Equal(t, "pending", response.Status)
	assert.Equal(t, 1, response.Priority)

	fmt.Printf("✓ Successfully retrieved task %s\n", response.ID)
}

func TestTaskDetailsSmoke_GetNonExistentTask(t *testing.T) {
	getResp, err := http.Get(baseURL + "/api/v2/tasks/nonexistent_id_12345")
	require.NoError(t, err, "Failed to make request - is the server running?")
	defer getResp.Body.Close()

	assert.Equal(t, http.StatusNotFound, getResp.StatusCode)

	var errorResp ErrorResponse
	json.NewDecoder(getResp.Body).Decode(&errorResp)
	assert.Contains(t, errorResp.Error, "not found")

	fmt.Printf("✓ Correctly returned 404 for non-existent task\n")
}

func TestTaskDetailsSmoke_GetTaskFields(t *testing.T) {
	reqBody := TaskRequest{
		Command:  "ls -la",
		Priority: 5,
	}
	body, _ := json.Marshal(reqBody)

	createResp, err := http.Post(baseURL+"/api/v2/tasks", "application/json", bytes.NewReader(body))
	require.NoError(t, err, "Failed to create task - is the server running?")
	defer createResp.Body.Close()

	var createdTask Task
	json.NewDecoder(createResp.Body).Decode(&createdTask)

	getResp, err := http.Get(baseURL + "/api/v2/tasks/" + createdTask.ID)
	require.NoError(t, err)
	defer getResp.Body.Close()

	var response Task
	json.NewDecoder(getResp.Body).Decode(&response)

	assert.NotEmpty(t, response.ID, "Task should have ID")
	assert.NotEmpty(t, response.Command, "Task should have Command")
	assert.NotEmpty(t, response.Status, "Task should have Status")
	assert.NotEmpty(t, response.CreatedAt, "Task should have CreatedAt")
	assert.Equal(t, 5, response.Priority, "Priority should match")

	fmt.Printf("✓ Task has all expected fields\n")
	fmt.Printf("  ID: %s\n", response.ID)
	fmt.Printf("  Command: %s\n", response.Command)
	fmt.Printf("  Status: %s\n", response.Status)
	fmt.Printf("  Priority: %d\n", response.Priority)
	fmt.Printf("  CreatedAt: %s\n", response.CreatedAt)
}
