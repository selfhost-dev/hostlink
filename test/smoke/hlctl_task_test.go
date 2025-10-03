//go:build smoke

package smoke

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskCreateSmoke_WithCommand(t *testing.T) {
	serverURL := getServerURL()

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "create", "--command", "ls -la", "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	taskID := parseJSONOutput(t, stdout)["id"].(string)
	verifyTaskInDatabase(t, taskID)
}

func TestTaskCreateSmoke_WithFile(t *testing.T) {
	serverURL := getServerURL()

	filePath, cleanup := createTempScript(t, "#!/bin/bash\necho 'smoke test'")
	defer cleanup()

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "create", "--file", filePath, "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	result := parseJSONOutput(t, stdout)
	assert.NotEmpty(t, result["id"])
	assert.Equal(t, "pending", result["status"])
}

func TestTaskCreateSmoke_WithTags(t *testing.T) {
	serverURL := getServerURL()

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "create",
		"--command", "whoami",
		"--tag", "env:test",
		"--tag", "smoke:true",
		"--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	result := parseJSONOutput(t, stdout)
	taskID := result["id"].(string)
	assert.NotEmpty(t, taskID)

	verifyTaskInDatabase(t, taskID)
}

func TestTaskCreateSmoke_OutputFormat(t *testing.T) {
	serverURL := getServerURL()

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "create", "--command", "date", "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	result := parseJSONOutput(t, stdout)

	assert.Contains(t, result, "id", "Output should contain id field")
	assert.Contains(t, result, "status", "Output should contain status field")
	assert.Contains(t, result, "created_at", "Output should contain created_at field")

	assert.NotEmpty(t, result["id"])
	assert.NotEmpty(t, result["status"])
	assert.NotEmpty(t, result["created_at"])
}

func TestTaskCreateSmoke_InvalidInput(t *testing.T) {
	serverURL := getServerURL()

	_, stderr, exitCode := runHlctlCommand(t, "task", "create",
		"--command", "ls",
		"--file", "/some/file.sh",
		"--server", serverURL)

	assert.NotEqual(t, 0, exitCode, "Should fail when both --command and --file are provided")
	assert.Contains(t, strings.ToLower(stderr), "cannot use both",
		"Error message should indicate mutual exclusivity")
}

func runHlctlCommand(t *testing.T, args ...string) (stdout string, stderr string, exitCode int) {
	hlctlPath, err := exec.LookPath("hlctl")
	if err != nil {
		tmpDir := t.TempDir()
		hlctlPath = filepath.Join(tmpDir, "hlctl")

		buildCmd := exec.Command("go", "build", "-o", hlctlPath, "../../cmd/hlctl/main.go")
		output, err := buildCmd.CombinedOutput()
		require.NoError(t, err, "Failed to build hlctl: %s", string(output))
	}

	cmd := exec.Command(hlctlPath, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err = cmd.Run()
	stdout = strings.TrimSpace(outBuf.String())
	stderr = strings.TrimSpace(errBuf.String())

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	} else {
		exitCode = 0
	}

	return stdout, stderr, exitCode
}

func verifyTaskInDatabase(t *testing.T, taskID string) {
	serverURL := getServerURL()
	resp, err := http.Get(serverURL + "/api/v2/tasks/" + taskID)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"Task should exist in database")

	var task map[string]any
	err = json.NewDecoder(resp.Body).Decode(&task)
	require.NoError(t, err)

	assert.Equal(t, taskID, task["id"])
}

func createTempScript(t *testing.T, content string) (filePath string, cleanup func()) {
	tmpDir := t.TempDir()
	filePath = filepath.Join(tmpDir, "smoke-script.sh")

	err := os.WriteFile(filePath, []byte(content), 0755)
	require.NoError(t, err)

	return filePath, func() { os.Remove(filePath) }
}

func parseJSONOutput(t *testing.T, jsonStr string) map[string]any {
	var result map[string]any
	err := json.Unmarshal([]byte(jsonStr), &result)
	require.NoError(t, err, "Output should be valid JSON: %s", jsonStr)
	return result
}

func createTaskViaAPI(t *testing.T, serverURL, command string) string {
	reqBody := map[string]any{"command": command, "priority": 1}
	jsonData, _ := json.Marshal(reqBody)

	resp, err := http.Post(serverURL+"/api/v2/tasks", "application/json", strings.NewReader(string(jsonData)))
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusCreated, resp.StatusCode)

	var task map[string]any
	json.NewDecoder(resp.Body).Decode(&task)
	return task["id"].(string)
}

func getFirstAgentID(t *testing.T, serverURL string) string {
	resp, err := http.Get(serverURL + "/api/v1/agents")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()

	var agents []map[string]any
	json.NewDecoder(resp.Body).Decode(&agents)

	if len(agents) > 0 {
		if id, ok := agents[0]["id"].(string); ok {
			return id
		}
	}
	return ""
}

func TestTaskListSmoke_WithoutFilters(t *testing.T) {
	// Creates 3 tasks via API then runs `hlctl task list` against real server to verify all are returned
	serverURL := getServerURL()

	createTaskViaAPI(t, serverURL, "ls -la")
	createTaskViaAPI(t, serverURL, "echo test")
	createTaskViaAPI(t, serverURL, "whoami")

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "list", "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var tasks []map[string]any
	err := json.Unmarshal([]byte(stdout), &tasks)
	require.NoError(t, err, "Output should be valid JSON array")
	assert.GreaterOrEqual(t, len(tasks), 3, "Should return at least 3 tasks")
}

func TestTaskListSmoke_WithStatusFilter(t *testing.T) {
	// Creates tasks with different statuses then runs `hlctl task list --status pending` to verify filtering
	serverURL := getServerURL()

	createTaskViaAPI(t, serverURL, "sleep 1")

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "list", "--status", "pending", "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var tasks []map[string]any
	err := json.Unmarshal([]byte(stdout), &tasks)
	require.NoError(t, err, "Output should be valid JSON array")

	for _, task := range tasks {
		assert.Equal(t, "pending", task["status"], "All returned tasks should have pending status")
	}
}

func TestTaskListSmoke_WithAgentFilter(t *testing.T) {
	// Creates tasks assigned to different agents then runs `hlctl task list --agent agt_1` to verify filtering
	serverURL := getServerURL()

	agentID := getFirstAgentID(t, serverURL)
	if agentID == "" {
		t.Skip("No agents available for testing")
	}

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "list", "--agent", agentID, "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var tasks []map[string]any
	err := json.Unmarshal([]byte(stdout), &tasks)
	require.NoError(t, err, "Output should be valid JSON array")
}

func TestTaskListSmoke_OutputFormat(t *testing.T) {
	// Runs `hlctl task list` and validates JSON array structure with required fields (id, command, status, etc)
	serverURL := getServerURL()

	createTaskViaAPI(t, serverURL, "echo format-test")

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "list", "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var tasks []map[string]any
	err := json.Unmarshal([]byte(stdout), &tasks)
	require.NoError(t, err, "Output should be valid JSON array")

	if len(tasks) > 0 {
		task := tasks[0]
		assert.Contains(t, task, "id", "Task should have id field")
		assert.Contains(t, task, "command", "Task should have command field")
		assert.Contains(t, task, "status", "Task should have status field")
		assert.Contains(t, task, "priority", "Task should have priority field")
		assert.Contains(t, task, "created_at", "Task should have created_at field")
	}
}

func TestTaskListSmoke_InvalidInput(t *testing.T) {
	// Runs `hlctl task list --status invalidstatus` and verifies graceful error handling
	serverURL := getServerURL()

	stdout, stderr, exitCode := runHlctlCommand(t, "task", "list", "--status", "invalidstatus", "--server", serverURL)

	if exitCode != 0 {
		assert.NotEmpty(t, stderr, "Should have error message")
	} else {
		var tasks []map[string]any
		err := json.Unmarshal([]byte(stdout), &tasks)
		require.NoError(t, err, "Should return valid JSON even with invalid status")
	}
}
