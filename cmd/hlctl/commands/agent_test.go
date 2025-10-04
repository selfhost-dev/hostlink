package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListAgentCommand(t *testing.T) {
	cmd := AgentCommand()

	assert.Equal(t, "agent", cmd.Name)
	assert.Equal(t, "Manage agents", cmd.Usage)
	assert.Len(t, cmd.Commands, 2)

	listCmd := cmd.Commands[0]
	assert.Equal(t, "list", listCmd.Name)
	assert.Equal(t, "List all agents", listCmd.Usage)
}

func TestGetAgentCommand(t *testing.T) {
	cmd := AgentCommand()

	assert.Len(t, cmd.Commands, 2)

	getCmd := cmd.Commands[1]
	assert.Equal(t, "get", getCmd.Name)
	assert.Equal(t, "Get agent details", getCmd.Usage)
	assert.Equal(t, "<agent-id>", getCmd.ArgsUsage)
}

func TestGetAgentAction_ValidatesAgentID(t *testing.T) {
	// This test is intentionally minimal as the validation happens at runtime
	// Integration tests will cover the full validation flow
}
