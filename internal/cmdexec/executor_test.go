package cmdexec

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestExecute_RunsCommand - verifies command is executed and output returned
func TestExecute_RunsCommand(t *testing.T) {
	executor := New()

	output, err := executor.Execute(context.Background(), "echo hello")

	assert.NoError(t, err)
	assert.Equal(t, "hello\n", output)
}

// TestExecute_ReturnsError - verifies error returned for failed command
func TestExecute_ReturnsError(t *testing.T) {
	executor := New()

	_, err := executor.Execute(context.Background(), "command_that_does_not_exist_12345")

	assert.Error(t, err)
}

// TestExecute_RespectsContext - verifies context cancellation stops execution
func TestExecute_RespectsContext(t *testing.T) {
	executor := New()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := executor.Execute(ctx, "sleep 5")

	assert.Error(t, err)
}
