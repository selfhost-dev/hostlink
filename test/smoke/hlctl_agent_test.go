//go:build smoke

package smoke

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentListSmoke_WithoutFilters(t *testing.T) {
	serverURL := getServerURL()

	stdout, stderr, exitCode := runHlctlCommand(t, "agent", "list", "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agents []map[string]any
	err := json.Unmarshal([]byte(stdout), &agents)
	require.NoError(t, err, "Output should be valid JSON array")
}

func TestAgentListSmoke_OutputFormat(t *testing.T) {
	serverURL := getServerURL()

	stdout, stderr, exitCode := runHlctlCommand(t, "agent", "list", "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agents []map[string]any
	err := json.Unmarshal([]byte(stdout), &agents)
	require.NoError(t, err, "Output should be valid JSON array")

	if len(agents) > 0 {
		agent := agents[0]
		assert.Contains(t, agent, "id", "Agent should have id field")
		assert.Contains(t, agent, "status", "Agent should have status field")
		assert.Contains(t, agent, "last_seen", "Agent should have last_seen field")
		assert.Contains(t, agent, "tags", "Agent should have tags field")
	}
}

func TestAgentListSmoke_EmptyResult(t *testing.T) {
	serverURL := getServerURL()

	stdout, _, exitCode := runHlctlCommand(t, "agent", "list", "--server", serverURL)

	assert.Equal(t, 0, exitCode)

	var agents []map[string]any
	err := json.Unmarshal([]byte(stdout), &agents)
	require.NoError(t, err, "Output should be valid JSON array even when empty")
}

func TestAgentGetSmoke_WithExistingAgent(t *testing.T) {
	serverURL := getServerURL()

	stdout, stderr, exitCode := runHlctlCommand(t, "agent", "list", "--server", serverURL)
	assert.Equal(t, 0, exitCode, "Failed to list agents: %s", stderr)

	var agents []map[string]any
	err := json.Unmarshal([]byte(stdout), &agents)
	require.NoError(t, err)

	if len(agents) == 0 {
		t.Skip("No agents available to test get command")
	}

	agentID := agents[0]["id"].(string)

	stdout, stderr, exitCode = runHlctlCommand(t, "agent", "get", agentID, "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agent map[string]any
	err = json.Unmarshal([]byte(stdout), &agent)
	require.NoError(t, err, "Output should be valid JSON")
	assert.Equal(t, agentID, agent["id"])
}

func TestAgentGetSmoke_WithNonExistentAgent(t *testing.T) {
	serverURL := getServerURL()

	_, stderr, exitCode := runHlctlCommand(t, "agent", "get", "agt_nonexistent_12345", "--server", serverURL)

	assert.NotEqual(t, 0, exitCode, "Should fail for non-existent agent")
	assert.NotEmpty(t, stderr)
}

func TestAgentGetSmoke_OutputFormat(t *testing.T) {
	serverURL := getServerURL()

	stdout, stderr, exitCode := runHlctlCommand(t, "agent", "list", "--server", serverURL)
	assert.Equal(t, 0, exitCode, "Failed to list agents: %s", stderr)

	var agents []map[string]any
	err := json.Unmarshal([]byte(stdout), &agents)
	require.NoError(t, err)

	if len(agents) == 0 {
		t.Skip("No agents available to test get command")
	}

	agentID := agents[0]["id"].(string)

	stdout, stderr, exitCode = runHlctlCommand(t, "agent", "get", agentID, "--server", serverURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agent map[string]any
	err = json.Unmarshal([]byte(stdout), &agent)
	require.NoError(t, err)

	assert.Contains(t, agent, "id", "Agent should have id field")
	assert.Contains(t, agent, "status", "Agent should have status field")
	assert.Contains(t, agent, "last_seen", "Agent should have last_seen field")
	assert.Contains(t, agent, "tags", "Agent should have tags field")
	assert.Contains(t, agent, "registered_at", "Agent should have registered_at field")
}
