//go:build integration
// +build integration

package integration

import (
	"os/exec"
	"strings"
	"testing"

	"hostlink/version"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHlctlBuild(t *testing.T) {
	cmd := exec.Command("go", "build", "-o", "hlctl-test", "../../cmd/hlctl")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to build hlctl: %s", string(output))

	t.Cleanup(func() {
		exec.Command("rm", "-f", "hlctl-test").Run()
	})
}

func TestHlctlHelp(t *testing.T) {
	cmd := exec.Command("go", "run", "../../cmd/hlctl", "--help")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to run hlctl --help: %s", string(output))

	outputStr := string(output)
	assert.Contains(t, outputStr, "hlctl", "Help should contain 'hlctl'")
	assert.Contains(t, outputStr, "USAGE", "Help should contain usage section")
}

func TestHlctlVersion(t *testing.T) {
	cmd := exec.Command("go", "run", "../../cmd/hlctl", "--version")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "Failed to run hlctl --version: %s", string(output))

	outputStr := strings.TrimSpace(string(output))
	assert.Contains(t, outputStr, version.Version, "Version output should contain version number")
}
