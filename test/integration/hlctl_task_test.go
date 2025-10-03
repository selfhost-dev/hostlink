package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskCreate_WithCommandFlag(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "task", "create", "--command", "ls -la", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assertJSONOutput(t, stdout, map[string]any{
		"id":     "non-empty",
		"status": "pending",
	})
}

func TestTaskCreate_WithFileFlag(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	filePath, cleanupFile := createTestScriptFile(t, "#!/bin/bash\necho 'test'")
	defer cleanupFile()

	stdout, stderr, exitCode := runHlctl(t, "task", "create", "--file", filePath, "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assertJSONOutput(t, stdout, map[string]any{
		"id":     "non-empty",
		"status": "pending",
	})
}

func TestTaskCreate_WithSingleTagFlag(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "task", "create", "--command", "ls", "--tag", "env:prod", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assertJSONOutput(t, stdout, map[string]any{
		"id":     "non-empty",
		"status": "pending",
	})
}

func TestTaskCreate_WithMultipleTagFlags(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "task", "create", "--command", "ls", "--tag", "env:prod", "--tag", "region:us", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assertJSONOutput(t, stdout, map[string]any{
		"id":     "non-empty",
		"status": "pending",
	})
}

func TestTaskCreate_WithPriorityFlag(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "task", "create", "--command", "ls", "--priority", "5", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assertJSONOutput(t, stdout, map[string]any{
		"id":     "non-empty",
		"status": "pending",
	})
}

func TestTaskCreate_WithDefaultPriority(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "task", "create", "--command", "ls", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assertJSONOutput(t, stdout, map[string]any{
		"id":     "non-empty",
		"status": "pending",
	})
}

func TestTaskCreate_WithoutFilters(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "task", "create", "--command", "echo hello", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assertJSONOutput(t, stdout, map[string]any{
		"id":     "non-empty",
		"status": "pending",
	})
}

func TestTaskCreate_WithAllFlags(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "task", "create",
		"--command", "ls -la",
		"--tag", "env:prod",
		"--tag", "region:us",
		"--priority", "3",
		"--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)
	assertJSONOutput(t, stdout, map[string]any{
		"id":     "non-empty",
		"status": "pending",
	})
}

func TestTaskCreate_ErrorBothCommandAndFile(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	filePath, cleanupFile := createTestScriptFile(t, "echo test")
	defer cleanupFile()

	_, stderr, exitCode := runHlctl(t, "task", "create", "--command", "ls", "--file", filePath, "--server", apiURL)

	assert.NotEqual(t, 0, exitCode)
	assertErrorOutput(t, stderr, "cannot use both")
}

func TestTaskCreate_ErrorNeitherCommandNorFile(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	_, stderr, exitCode := runHlctl(t, "task", "create", "--server", apiURL)

	assert.NotEqual(t, 0, exitCode)
	assertErrorOutput(t, stderr, "must provide either")
}

func TestTaskCreate_ErrorFileDoesNotExist(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	_, stderr, exitCode := runHlctl(t, "task", "create", "--file", "/nonexistent/file.sh", "--server", apiURL)

	assert.NotEqual(t, 0, exitCode)
	assertErrorOutput(t, stderr, "no such file")
}

func TestTaskCreate_ErrorAPIUnreachable(t *testing.T) {
	_, stderr, exitCode := runHlctl(t, "task", "create", "--command", "ls", "--server", "http://localhost:99999")

	assert.NotEqual(t, 0, exitCode)
	assertErrorOutput(t, stderr, "failed to create task")
}

func TestTaskCreate_OutputsJSON(t *testing.T) {
	apiURL, cleanup := startTestAPI(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "task", "create", "--command", "ls", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var result map[string]any
	err := json.Unmarshal([]byte(stdout), &result)
	require.NoError(t, err, "Output should be valid JSON")

	assert.Contains(t, result, "id")
	assert.Contains(t, result, "status")
	assert.Contains(t, result, "created_at")
}

func runHlctl(t *testing.T, args ...string) (stdout string, stderr string, exitCode int) {
	hlctlPath := buildHlctl(t)

	cmd := exec.Command(hlctlPath, args...)
	var outBuf, errBuf strings.Builder
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
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

func startTestAPI(t *testing.T) (baseURL string, cleanup func()) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/v2/tasks" {
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]any{
				"id":         "task-test-123",
				"status":     "pending",
				"created_at": "2025-10-03T00:00:00Z",
			})
			return
		}

		if r.Method == "GET" && r.URL.Path == "/api/v1/agents" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "agt_1"},
				{"id": "agt_2"},
			})
			return
		}

		w.WriteHeader(http.StatusNotFound)
	}))

	return server.URL, server.Close
}

func createTestScriptFile(t *testing.T, content string) (filePath string, cleanup func()) {
	tmpDir := t.TempDir()
	filePath = filepath.Join(tmpDir, "test-script.sh")

	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	return filePath, func() { os.Remove(filePath) }
}

func assertJSONOutput(t *testing.T, output string, expectedFields map[string]any) {
	var result map[string]any
	err := json.Unmarshal([]byte(output), &result)
	require.NoError(t, err, "Output should be valid JSON")

	for field, expectedValue := range expectedFields {
		actualValue, exists := result[field]
		require.True(t, exists, "Field %s should exist in JSON output", field)

		if expectedValue == "non-empty" {
			assert.NotEmpty(t, actualValue, "Field %s should not be empty", field)
		} else {
			assert.Equal(t, expectedValue, actualValue, "Field %s should match expected value", field)
		}
	}
}

func assertErrorOutput(t *testing.T, stderr string, expectedErrorMsg string) {
	assert.Contains(t, strings.ToLower(stderr), strings.ToLower(expectedErrorMsg),
		"Error output should contain expected message")
}

func buildHlctl(t *testing.T) string {
	tmpDir := t.TempDir()
	hlctlPath := filepath.Join(tmpDir, "hlctl")

	ctx, cancel := context.WithTimeout(context.Background(), 30*1000000000)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "-o", hlctlPath, "../../cmd/hlctl/main.go")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build hlctl: %s", string(output))

	return hlctlPath
}
