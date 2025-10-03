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

	assert.Equal(t, taskID, task["ID"])
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
