//go:build integration
// +build integration

package integration

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"hostlink/internal/update"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildUpdaterBinary compiles the hostlink-updater binary into the given directory.
// Returns the path to the compiled binary.
func buildUpdaterBinary(t *testing.T, outputDir string) string {
	t.Helper()
	binaryPath := filepath.Join(outputDir, "hostlink-updater")
	cmd := exec.Command("go", "build", "-o", binaryPath, "./cmd/updater")
	cmd.Dir = findProjectRoot(t)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to build hostlink-updater: %s", string(output))
	return binaryPath
}

// findProjectRoot returns the project root directory by looking for go.mod.
func findProjectRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find project root (no go.mod found)")
		}
		dir = parent
	}
}

// setupUpdateDirs creates the update directory structure in a temp dir.
// Returns the base directory path.
func setupUpdateDirs(t *testing.T) string {
	t.Helper()
	baseDir := t.TempDir()
	err := update.InitDirectories(baseDir)
	require.NoError(t, err)
	return baseDir
}

// writeStateFile writes a state.json file into the base directory.
func writeStateFile(t *testing.T, baseDir string, data update.StateData) {
	t.Helper()
	paths := update.NewPaths(baseDir)
	sw := update.NewStateWriter(update.StateConfig{StatePath: paths.StateFile})
	err := sw.Write(data)
	require.NoError(t, err)
}

// readStateFile reads and returns the state.json contents from the base directory.
func readStateFile(t *testing.T, baseDir string) update.StateData {
	t.Helper()
	paths := update.NewPaths(baseDir)
	sw := update.NewStateWriter(update.StateConfig{StatePath: paths.StateFile})
	data, err := sw.Read()
	require.NoError(t, err)
	return data
}

func TestSelfUpdate_LockPreventsConcurrentUpdates(t *testing.T) {
	baseDir := setupUpdateDirs(t)
	paths := update.NewPaths(baseDir)

	// Acquire lock from this process
	lock := update.NewLockManager(update.LockConfig{LockPath: paths.LockFile})
	err := lock.TryLock(5 * time.Minute)
	require.NoError(t, err)
	defer lock.Unlock()

	// Build the updater binary
	binDir := t.TempDir()
	updaterBin := buildUpdaterBinary(t, binDir)

	// Write state file so updater can read target version
	writeStateFile(t, baseDir, update.StateData{
		State:         update.StateStaged,
		TargetVersion: "1.2.3",
	})

	// Attempt to run the updater — it should fail because the lock is held
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, updaterBin,
		"-base-dir", baseDir,
		"-version", "1.2.3",
	)
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "updater should fail when lock is held")
	assert.Contains(t, string(output), "lock", "error should mention lock")
}

func TestSelfUpdate_SignalHandlingDuringUpdate(t *testing.T) {
	baseDir := setupUpdateDirs(t)

	// Build the updater binary
	binDir := t.TempDir()
	updaterBin := buildUpdaterBinary(t, binDir)

	// Write state file with target version
	writeStateFile(t, baseDir, update.StateData{
		State:         update.StateStaged,
		TargetVersion: "1.2.3",
	})

	// Start the updater process — it will try to stop the service via systemctl
	// which will take time / fail, giving us time to send a signal
	cmd := exec.Command(updaterBin,
		"-base-dir", baseDir,
		"-version", "1.2.3",
		"-binary", "/nonexistent/hostlink", // non-existent binary, will fail at stop
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err := cmd.Start()
	require.NoError(t, err, "should start updater process")

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// Send SIGTERM
	err = cmd.Process.Signal(syscall.SIGTERM)
	require.NoError(t, err, "should send SIGTERM")

	// Wait for exit — should exit within a few seconds
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		// Process exited (may be non-zero exit code due to cancellation, that's ok)
		if err != nil {
			// Verify it's an exit error, not something unexpected
			_, ok := err.(*exec.ExitError)
			assert.True(t, ok, "expected ExitError after signal, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("updater did not exit within 10 seconds after SIGTERM")
	}
}

func TestSelfUpdate_UpdaterWritesStateOnLockFailure(t *testing.T) {
	baseDir := setupUpdateDirs(t)
	paths := update.NewPaths(baseDir)

	// Acquire lock from this process to block the updater
	lock := update.NewLockManager(update.LockConfig{LockPath: paths.LockFile})
	err := lock.TryLock(5 * time.Minute)
	require.NoError(t, err)
	defer lock.Unlock()

	// Build the updater
	binDir := t.TempDir()
	updaterBin := buildUpdaterBinary(t, binDir)

	// Run the updater — should fail on lock acquisition
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, updaterBin,
		"-base-dir", baseDir,
		"-version", "2.0.0",
	)
	_, err = cmd.CombinedOutput()
	require.Error(t, err, "updater should fail when lock is held")

	// The lock file should still be held by us (not stolen)
	lockContent, err := os.ReadFile(paths.LockFile)
	require.NoError(t, err)

	var lockData struct {
		PID int `json:"pid"`
	}
	err = json.Unmarshal(lockContent, &lockData)
	require.NoError(t, err)
	assert.Equal(t, os.Getpid(), lockData.PID, "lock should still be held by test process")
}

func TestSelfUpdate_UpdaterExitsWithErrorForMissingVersion(t *testing.T) {
	baseDir := setupUpdateDirs(t)

	// Build the updater
	binDir := t.TempDir()
	updaterBin := buildUpdaterBinary(t, binDir)

	// Run without -version and without state file — should fail
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, updaterBin,
		"-base-dir", baseDir,
	)
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "updater should fail without version")
	assert.Contains(t, string(output), "version", "error should mention version")
}

func TestSelfUpdate_UpdaterReadsVersionFromState(t *testing.T) {
	baseDir := setupUpdateDirs(t)

	// Write state file with target version
	writeStateFile(t, baseDir, update.StateData{
		State:         update.StateStaged,
		TargetVersion: "3.0.0",
	})

	// Build the updater
	binDir := t.TempDir()
	updaterBin := buildUpdaterBinary(t, binDir)

	// Run without -version flag but with state file containing version
	// It should read the version from state and proceed (then fail at systemctl stop)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, updaterBin,
		"-base-dir", baseDir,
		"-binary", "/nonexistent/hostlink",
	)
	output, err := cmd.CombinedOutput()
	// Should fail (no systemctl), but NOT because of missing version
	require.Error(t, err)
	assert.NotContains(t, string(output), "target version is required",
		"should have read version from state file")
}

func TestSelfUpdate_UpdaterPrintVersion(t *testing.T) {
	// Build the updater
	binDir := t.TempDir()
	updaterBin := buildUpdaterBinary(t, binDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, updaterBin, "-v")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "version flag should not fail")
	assert.Contains(t, string(output), "hostlink-updater", "should print version info")
}
