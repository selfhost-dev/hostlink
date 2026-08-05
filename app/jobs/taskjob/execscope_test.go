package taskjob

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildTaskCmd_PlainFallback - without systemd-run the command is a plain
// /bin/sh -c invocation (previous behavior).
func TestBuildTaskCmd_PlainFallback(t *testing.T) {
	old := systemdRunPath
	systemdRunPath = ""
	defer func() { systemdRunPath = old }()

	cmd := buildTaskCmd("/tmp/x_script.sh")

	assert.Equal(t, "/bin/sh", cmd.Path)
	assert.Equal(t, []string{"/bin/sh", "-c", "/tmp/x_script.sh"}, cmd.Args)
}

// TestBuildTaskCmd_ScopeArgs - with systemd-run present the script runs in a
// transient scope, not as a direct child of hostlink.
func TestBuildTaskCmd_ScopeArgs(t *testing.T) {
	old := systemdRunPath
	systemdRunPath = "/usr/bin/systemd-run"
	defer func() { systemdRunPath = old }()

	cmd := buildTaskCmd("/tmp/x_script.sh")

	assert.Equal(t, "/usr/bin/systemd-run", cmd.Path)
	require.Len(t, cmd.Args, 7)
	assert.Equal(t, "--scope", cmd.Args[1])
	assert.Equal(t, "--quiet", cmd.Args[2])
	assert.True(t, strings.HasPrefix(cmd.Args[3], "--unit=hostlink-task-"), "unit name: %s", cmd.Args[3])
	assert.False(t, strings.HasSuffix(cmd.Args[3], ".scope"), "systemd appends .scope itself: %s", cmd.Args[3])
	assert.Equal(t, "/bin/sh", cmd.Args[4])
	assert.Equal(t, "-c", cmd.Args[5])
	assert.Equal(t, "/tmp/x_script.sh", cmd.Args[6])
}

// TestBuildTaskCmd_UniqueUnitNames - concurrent tasks must not collide on the
// transient scope unit name.
func TestBuildTaskCmd_UniqueUnitNames(t *testing.T) {
	old := systemdRunPath
	systemdRunPath = "/usr/bin/systemd-run"
	defer func() { systemdRunPath = old }()

	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		cmd := buildTaskCmd("/tmp/x_script.sh")
		unit := cmd.Args[3]
		assert.False(t, seen[unit], "duplicate unit name: %s", unit)
		seen[unit] = true
	}
}

// TestBuildTaskCmd_ScopeIsolation - integration: with a real systemd (Vagrant /
// systemd hosts) the task must land in its own scope cgroup, NOT the
// hostlink.service cgroup, and exit code must propagate. Skipped when
// systemd-run is absent or no systemd bus is reachable (containers, macOS).
func TestBuildTaskCmd_ScopeIsolation(t *testing.T) {
	if systemdRunPath == "" {
		t.Skip("systemd-run not available")
	}
	probe := exec.Command(systemdRunPath, "--scope", "--quiet", "/bin/true")
	if err := probe.Run(); err != nil {
		t.Skipf("systemd bus not reachable: %v", err)
	}

	cmd := buildTaskCmd("/bin/sh -c 'cat /proc/self/cgroup'")
	out, err := cmd.Output()
	require.NoError(t, err)

	cgroup := strings.TrimSpace(string(out))
	assert.Contains(t, cgroup, "hostlink-task-", "task must run in its own scope cgroup, got: %s", cgroup)

	// Exit code propagation through the scope.
	fail := buildTaskCmd("/bin/sh -c 'exit 42'")
	runErr := fail.Run()
	var exitErr *exec.ExitError
	require.ErrorAs(t, runErr, &exitErr)
	assert.Equal(t, 42, exitErr.ExitCode())
}
