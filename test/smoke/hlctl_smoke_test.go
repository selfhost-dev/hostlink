//go:build integration
// +build integration

package smoke

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHlctlSmoke(t *testing.T) {
	cmd := exec.Command("go", "run", "../../cmd/hlctl")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to run hlctl: %s", string(output))

	outputStr := string(output)
	assert.Contains(t, outputStr, "hlctl", "Default output should contain 'hlctl'")
}
