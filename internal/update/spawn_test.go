package update

import (
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpawnUpgrade_StartsProcess(t *testing.T) {
	err := SpawnUpgrade("/bin/sleep", []string{"0.1"})
	require.NoError(t, err)
}

func TestSpawnUpgrade_SetpgidTrue(t *testing.T) {
	// Use a process that prints its own PGID so we can verify
	// We spawn "sleep 2" and check its PGID differs from ours
	cmd, err := spawnWithCmd("/bin/sleep", []string{"0.5"})
	require.NoError(t, err)
	require.NotNil(t, cmd)
	require.NotNil(t, cmd.Process)

	defer cmd.Process.Kill()
	defer cmd.Wait()

	childPID := cmd.Process.Pid
	childPGID, err := syscall.Getpgid(childPID)
	require.NoError(t, err)

	parentPGID, err := syscall.Getpgid(os.Getpid())
	require.NoError(t, err)

	assert.NotEqual(t, parentPGID, childPGID,
		"child PGID should differ from parent PGID (Setpgid: true)")
	assert.Equal(t, childPID, childPGID,
		"child should be its own process group leader (PGID == PID)")
}

func TestSpawnUpgrade_ReturnsErrorForInvalidBinary(t *testing.T) {
	err := SpawnUpgrade("/nonexistent/binary", []string{})
	assert.Error(t, err)
}
