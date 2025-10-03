package commands

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestListAgentCommand(t *testing.T) {
	cmd := AgentCommand()

	assert.Equal(t, "agent", cmd.Name)
	assert.Equal(t, "Manage agents", cmd.Usage)
	assert.Len(t, cmd.Commands, 1)

	listCmd := cmd.Commands[0]
	assert.Equal(t, "list", listCmd.Name)
	assert.Equal(t, "List all agents", listCmd.Usage)
}
