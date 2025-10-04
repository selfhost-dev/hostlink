//go:build integration
// +build integration

package integration

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostlink/app"
	"hostlink/app/controller/tasks"
	"hostlink/config"
	"hostlink/domain/agent"
	"hostlink/domain/task"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Test Scenario 1: Full task lifecycle
func TestE2E_FullTaskLifecycle(t *testing.T) {
	t.Run("should create task via hlctl, agent polls and executes, verify output via hlctl task get", func(t *testing.T) {
		env := setupE2EHlctlEnv(t)

		agentInfo := createAgentWithTags(t, env, "e2e-test-agent", map[string]string{})

		stdout, stderr, exitCode := runHlctlCommand(t, env.serverURL, "task", "create", "--command", "echo 'e2e test output'")
		require.Equal(t, 0, exitCode, "stderr: %s", stderr)

		var createResp map[string]any
		err := json.Unmarshal([]byte(stdout), &createResp)
		require.NoError(t, err)
		taskID := createResp["id"].(string)
		require.NotEmpty(t, taskID)

		tasks := simulateAgentPoll(t, env, agentInfo)
		require.Len(t, tasks, 1)
		assert.Equal(t, taskID, tasks[0].ID)

		cmdStdout, cmdStderr, cmdExitCode := executeCommand(t, tasks[0].Command)

		updateTaskStatus(t, env, agentInfo, taskID, "completed", cmdStdout+cmdStderr, cmdExitCode)

		stdout, stderr, exitCode = runHlctlCommand(t, env.serverURL, "task", "get", taskID)
		require.Equal(t, 0, exitCode, "stderr: %s", stderr)

		var getResp map[string]any
		err = json.Unmarshal([]byte(stdout), &getResp)
		require.NoError(t, err)
		assert.Equal(t, "completed", getResp["status"])
		assert.Contains(t, getResp["output"], "e2e test output")
		assert.Equal(t, float64(0), getResp["exit_code"])
	})
}

// Test Scenario 4: Error scenarios - failing command
func TestE2E_ErrorScenarios_FailingCommand(t *testing.T) {
	t.Run("should capture exit code and stderr from failing command", func(t *testing.T) {
		env := setupE2EHlctlEnv(t)

		agentInfo := createAgentWithTags(t, env, "e2e-error-agent", map[string]string{})

		stdout, stderr, exitCode := runHlctlCommand(t, env.serverURL, "task", "create", "--command", "ls /nonexistent-directory-e2e-test")
		require.Equal(t, 0, exitCode, "stderr: %s", stderr)

		var createResp map[string]any
		err := json.Unmarshal([]byte(stdout), &createResp)
		require.NoError(t, err)
		taskID := createResp["id"].(string)

		tasks := simulateAgentPoll(t, env, agentInfo)
		require.Len(t, tasks, 1)

		cmdStdout, cmdStderr, cmdExitCode := executeCommand(t, tasks[0].Command)

		updateTaskStatus(t, env, agentInfo, taskID, "failed", cmdStdout+cmdStderr, cmdExitCode)

		stdout, stderr, exitCode = runHlctlCommand(t, env.serverURL, "task", "get", taskID)
		require.Equal(t, 0, exitCode, "stderr: %s", stderr)

		var getResp map[string]any
		err = json.Unmarshal([]byte(stdout), &getResp)
		require.NoError(t, err)
		assert.Equal(t, "failed", getResp["status"])
		assert.NotEqual(t, float64(0), getResp["exit_code"])
		assert.Contains(t, strings.ToLower(getResp["output"].(string)), "no such file")
	})
}

// Test Scenario 5: Agent management
func TestE2E_AgentManagement(t *testing.T) {
	t.Run("should list and get agent details", func(t *testing.T) {
		env := setupE2EHlctlEnv(t)

		agent1Info := createAgentWithTags(t, env, "e2e-mgmt-agent-1", map[string]string{
			"env": "prod",
			"app": "web",
		})

		agent2Info := createAgentWithTags(t, env, "e2e-mgmt-agent-2", map[string]string{
			"env": "staging",
			"app": "api",
		})

		stdout, stderr, exitCode := runHlctlCommand(t, env.serverURL, "agent", "list")
		require.Equal(t, 0, exitCode, "stderr: %s", stderr)

		var listResp []map[string]any
		err := json.Unmarshal([]byte(stdout), &listResp)
		require.NoError(t, err)
		assert.Len(t, listResp, 2)

		stdout, stderr, exitCode = runHlctlCommand(t, env.serverURL, "agent", "get", agent1Info.agentID)
		require.Equal(t, 0, exitCode, "stderr: %s", stderr)

		var getResp map[string]any
		err = json.Unmarshal([]byte(stdout), &getResp)
		require.NoError(t, err)
		assert.Equal(t, agent1Info.agentID, getResp["id"])
		assert.NotNil(t, getResp["tags"])

		tags := getResp["tags"].([]any)
		assert.Len(t, tags, 2)

		_ = agent2Info
	})
}

// Test Scenario 6: File-based task
func TestE2E_FileBasedTask(t *testing.T) {
	t.Run("should create and execute task from script file", func(t *testing.T) {
		env := setupE2EHlctlEnv(t)

		agentInfo := createAgentWithTags(t, env, "e2e-file-agent", map[string]string{})

		scriptContent := `#!/bin/bash
echo "Line 1: Starting script"
echo "Line 2: Processing"
echo "Line 3: Completed"
`
		scriptPath, cleanup := createE2ETestScriptFile(t, scriptContent)
		defer cleanup()

		stdout, stderr, exitCode := runHlctlCommand(t, env.serverURL, "task", "create", "--file", scriptPath)
		require.Equal(t, 0, exitCode, "stderr: %s", stderr)

		var createResp map[string]any
		err := json.Unmarshal([]byte(stdout), &createResp)
		require.NoError(t, err)
		taskID := createResp["id"].(string)

		tasks := simulateAgentPoll(t, env, agentInfo)
		require.Len(t, tasks, 1)

		cmdStdout, cmdStderr, cmdExitCode := executeCommand(t, tasks[0].Command)

		updateTaskStatus(t, env, agentInfo, taskID, "completed", cmdStdout+cmdStderr, cmdExitCode)

		stdout, stderr, exitCode = runHlctlCommand(t, env.serverURL, "task", "get", taskID)
		require.Equal(t, 0, exitCode, "stderr: %s", stderr)

		var getResp map[string]any
		err = json.Unmarshal([]byte(stdout), &getResp)
		require.NoError(t, err)
		assert.Equal(t, "completed", getResp["status"])
		assert.Contains(t, getResp["output"], "Line 1: Starting script")
		assert.Contains(t, getResp["output"], "Line 2: Processing")
		assert.Contains(t, getResp["output"], "Line 3: Completed")
	})
}

// Helper: E2E environment setup
type e2eHlctlEnv struct {
	echo      *echo.Echo
	db        *gorm.DB
	container *app.Container
	server    *httptest.Server
	serverURL string
}

type e2eAgentInfo struct {
	agentID    string
	privateKey *rsa.PrivateKey
}

func setupE2EHlctlEnv(t *testing.T) *e2eHlctlEnv {
	t.Helper()

	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	require.NoError(t, err)

	container := app.NewContainer(db)
	err = container.Migrate()
	require.NoError(t, err)

	e := echo.New()
	e.Validator = &e2eHlctlValidator{}
	config.AddRoutesV2(e, container)

	server := httptest.NewServer(e)

	env := &e2eHlctlEnv{
		db:        db,
		echo:      e,
		container: container,
		server:    server,
		serverURL: server.URL,
	}

	t.Cleanup(func() {
		server.Close()
		sqlDB, err := db.DB()
		if err == nil {
			sqlDB.Close()
		}
	})

	return env
}

type e2eHlctlValidator struct{}

func (v *e2eHlctlValidator) Validate(i interface{}) error {
	return nil
}

// Helper: Run hlctl command
func runHlctlCommand(t *testing.T, serverURL string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()

	hlctlPath := buildE2EHlctl(t)

	allArgs := append([]string{"--server", serverURL}, args...)
	cmd := exec.Command(hlctlPath, allArgs...)

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

func buildE2EHlctl(t *testing.T) string {
	t.Helper()

	tmpDir := t.TempDir()
	hlctlPath := filepath.Join(tmpDir, "hlctl")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "build", "-o", hlctlPath, "../../cmd/hlctl/main.go")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build hlctl: %s", string(output))

	return hlctlPath
}

// Helper: Simulate agent polling and task execution
func simulateAgentPoll(t *testing.T, env *e2eHlctlEnv, agentInfo *e2eAgentInfo) []*task.Task {
	t.Helper()

	req := createSignedE2ERequest(t, http.MethodGet, "/api/v1/tasks", agentInfo.agentID, agentInfo.privateKey, time.Now())
	rec := httptest.NewRecorder()

	env.echo.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var tasks []*task.Task
	err := json.Unmarshal(rec.Body.Bytes(), &tasks)
	require.NoError(t, err)

	return tasks
}

// Helper: Create agent with tags
func createAgentWithTags(t *testing.T, env *e2eHlctlEnv, fingerprint string, tags map[string]string) *e2eAgentInfo {
	t.Helper()

	privateKey, publicKeyBase64 := generateE2EKeyPair(t)

	agentTags := make([]agent.AgentTag, 0, len(tags))
	for key, value := range tags {
		agentTags = append(agentTags, agent.AgentTag{Key: key, Value: value})
	}

	testAgent := &agent.Agent{
		PublicKey:     publicKeyBase64,
		PublicKeyType: "rsa",
		Fingerprint:   fingerprint,
		Tags:          agentTags,
	}

	err := env.container.AgentRepository.Create(context.Background(), testAgent)
	require.NoError(t, err)

	return &e2eAgentInfo{
		agentID:    testAgent.ID,
		privateKey: privateKey,
	}
}

// Helper: Execute command and capture output
func executeCommand(t *testing.T, command string) (stdout, stderr string, exitCode int) {
	t.Helper()

	cmd := exec.Command("sh", "-c", command)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

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

// Helper: Update task status and output
func updateTaskStatus(t *testing.T, env *e2eHlctlEnv, agentInfo *e2eAgentInfo, taskID string, status string, output string, exitCode int) {
	t.Helper()

	updateReq := tasks.TaskUpdateRequest{
		Status:   status,
		Output:   output,
		ExitCode: exitCode,
	}
	body, _ := json.Marshal(updateReq)

	req := createSignedRequestWithBody(t, http.MethodPut, fmt.Sprintf("/api/v1/tasks/%s", taskID), agentInfo.agentID, agentInfo.privateKey, time.Now(), bytes.NewReader(body))
	rec := httptest.NewRecorder()

	env.echo.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
}

// Helper: Create test script file
func createE2ETestScriptFile(t *testing.T, content string) (filePath string, cleanup func()) {
	t.Helper()

	tmpDir := t.TempDir()
	filePath = filepath.Join(tmpDir, "test-script.sh")

	err := os.WriteFile(filePath, []byte(content), 0644)
	require.NoError(t, err)

	return filePath, func() { os.Remove(filePath) }
}
