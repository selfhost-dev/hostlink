//go:build smoke

package smoke

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentTaskExecutionE2E(t *testing.T) {
	serverURL := getServerURL()

	taskID := createTaskViaAPI(t, serverURL, "echo 'e2e test'")

	time.Sleep(5 * time.Second)

	task := getTaskFromAPI(t, serverURL, taskID)

	assert.Equal(t, "completed", task["status"], "Task should be completed")
	assert.Contains(t, task["output"], "e2e test", "Output should contain expected text")
	assert.Equal(t, float64(0), task["exit_code"], "Exit code should be 0")
}

func TestTaskCreationAndFetch(t *testing.T) {
	serverURL := getServerURL()

	taskID := createTaskViaAPI(t, serverURL, "ls -la")

	task := getTaskFromAPI(t, serverURL, taskID)

	assert.Equal(t, taskID, task["id"])
	assert.Equal(t, "pending", task["status"], "New task should have pending status")
	assert.Contains(t, task, "command")
}

func TestTaskExecutionAndReport(t *testing.T) {
	serverURL := getServerURL()

	taskID := createTaskViaAPI(t, serverURL, "echo hello")

	time.Sleep(5 * time.Second)

	task := getTaskFromAPI(t, serverURL, taskID)

	assert.Equal(t, "completed", task["status"], "Task should be completed after execution")
}

func TestTaskOutputCapture(t *testing.T) {
	serverURL := getServerURL()

	taskID := createTaskViaAPI(t, serverURL, "echo 'specific output 12345'")

	time.Sleep(5 * time.Second)

	task := getTaskFromAPI(t, serverURL, taskID)

	output, ok := task["output"].(string)
	require.True(t, ok, "Output should be a string")
	assert.Contains(t, output, "specific output 12345", "Output should contain the expected text")
}

func TestTaskExitCodeCapture(t *testing.T) {
	serverURL := getServerURL()

	taskID := createTaskViaAPI(t, serverURL, "exit 42")

	time.Sleep(5 * time.Second)

	task := getTaskFromAPI(t, serverURL, taskID)

	assert.Equal(t, float64(42), task["exit_code"], "Exit code should be captured correctly")
}

func TestTaskErrorCapture(t *testing.T) {
	serverURL := getServerURL()

	taskID := createTaskViaAPI(t, serverURL, "nonexistent-command-xyz")

	time.Sleep(5 * time.Second)

	task := getTaskFromAPI(t, serverURL, taskID)

	errorMsg, ok := task["error"].(string)
	require.True(t, ok, "Error should be a string")
	assert.NotEmpty(t, errorMsg, "Error message should be populated for failed command")
}

func TestHlctlShowsTaskOutput(t *testing.T) {
	serverURL := getServerURL()

	stdout, _, _ := runHlctlCommand(t, "task", "create", "--command", "echo 'hlctl test output'", "--server", serverURL)
	createResult := parseJSONOutput(t, stdout)
	taskID := createResult["id"].(string)

	time.Sleep(5 * time.Second)

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "get", taskID, "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	result := parseJSONOutput(t, stdout)
	output, ok := result["output"].(string)
	require.True(t, ok, "Output should be present")
	assert.Contains(t, output, "hlctl test output", "hlctl should display the task output")
}

func getTaskFromAPI(t *testing.T, serverURL, taskID string) map[string]any {
	t.Helper()

	resp, err := http.Get(serverURL + "/api/v2/tasks/" + taskID)
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode, "Task should exist")

	var task map[string]any
	err = json.NewDecoder(resp.Body).Decode(&task)
	require.NoError(t, err)

	return task
}
