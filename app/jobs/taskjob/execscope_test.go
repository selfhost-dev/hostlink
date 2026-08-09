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
	cmd := buildTaskCmdWithRun("/tmp/x_script.sh", "")

	assert.Equal(t, "/bin/sh", cmd.Path)
	assert.Equal(t, []string{"/bin/sh", "-c", "/tmp/x_script.sh"}, cmd.Args)
}

// TestBuildTaskCmd_ScopeArgs - with systemd-run present the script runs in a
// transient scope, not as a direct child of hostlink.
func TestBuildTaskCmd_ScopeArgs(t *testing.T) {
	cmd := buildTaskCmdWithRun("/tmp/x_script.sh", "/usr/bin/systemd-run")

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
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		cmd := buildTaskCmdWithRun("/tmp/x_script.sh", "/usr/bin/systemd-run")
		unit := cmd.Args[3]
		assert.False(t, seen[unit], "duplicate unit name: %s", unit)
		seen[unit] = true
	}
}

// TestProbeSystemdRun - the functional probe must reject a systemd-run binary
// without a working systemd (CI containers), not just its presence.
func TestProbeSystemdRun(t *testing.T) {
	path, err := exec.LookPath("systemd-run")
	if err != nil {
		// No binary at all -> no systemd.
		assert.Equal(t, "", probeSystemdRun())
		return
	}
	got := probeSystemdRun()
	if got == path {
		// A working systemd is present; probe must return the binary.
		return
	}
	// Binary exists but systemd does not work (CI) -> must fall back.
	assert.Equal(t, "", got, "systemd-run probe must fail without a running systemd")
}
