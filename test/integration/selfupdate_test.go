//go:build integration && linux

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

// buildHostlinkBinary compiles the hostlink binary into the given directory.
// Returns the path to the compiled binary.
func buildHostlinkBinary(t *testing.T, outputDir string) string {
	t.Helper()
	binaryPath := filepath.Join(outputDir, "hostlink")
	cmd := exec.Command("go", "build", "-o", binaryPath, ".")
	cmd.Dir = findProjectRoot(t)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "failed to build hostlink: %s", string(output))
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

// readStateFile reads and returns the state.json contents from the base directory.
func readStateFile(t *testing.T, baseDir string) update.StateData {
	t.Helper()
	paths := update.NewPaths(baseDir)
	sw := update.NewStateWriter(update.StateConfig{StatePath: paths.StateFile})
	data, err := sw.Read()
	require.NoError(t, err)
	return data
}

// createDummyBinary creates a dummy executable file at the given path.
// Returns the path to the created file.
func createDummyBinary(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "hostlink")
	err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755)
	require.NoError(t, err)
	return path
}

// testEnv returns the environment variables for running hostlink in test mode.
// This sets HOSTLINK_ENV=test to skip auto-download behavior.
func testEnv() []string {
	return append(os.Environ(), "HOSTLINK_ENV=test")
}

func TestUpgrade_LockPreventsConcurrent(t *testing.T) {
	baseDir := setupUpdateDirs(t)
	paths := update.NewPaths(baseDir)

	// Acquire lock from this process
	lock := update.NewLockManager(update.LockConfig{LockPath: paths.LockFile})
	err := lock.TryLock(5 * time.Minute)
	require.NoError(t, err)
	defer lock.Unlock()

	// Build the hostlink binary
	binDir := t.TempDir()
	hostlinkBin := buildHostlinkBinary(t, binDir)

	// Attempt to run upgrade — it should fail because the lock is held
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, hostlinkBin, "upgrade",
		"--base-dir", baseDir,
		"--install-path", "/tmp/fake-hostlink",
	)
	cmd.Env = testEnv()
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "upgrade should fail when lock is held")
	assert.Contains(t, string(output), "lock", "error should mention lock")

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

func TestUpgrade_SignalHandling(t *testing.T) {
	baseDir := setupUpdateDirs(t)

	// Build the hostlink binary
	binDir := t.TempDir()
	hostlinkBin := buildHostlinkBinary(t, binDir)

	// Create a dummy binary at install-path so backup succeeds,
	// then systemctl stop will stall/fail giving us time to send a signal
	installDir := t.TempDir()
	installPath := createDummyBinary(t, installDir)

	// Start the upgrade process
	cmd := exec.Command(hostlinkBin, "upgrade",
		"--base-dir", baseDir,
		"--install-path", installPath,
	)
	cmd.Env = testEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	err := cmd.Start()
	require.NoError(t, err, "should start upgrade process")

	// Give it time to start and reach the systemctl stop phase
	time.Sleep(500 * time.Millisecond)

	// Send SIGTERM
	err = cmd.Process.Signal(syscall.SIGTERM)
	require.NoError(t, err, "should send SIGTERM")

	// Wait for exit — should exit within 10 seconds
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		// Process exited (non-zero exit due to cancellation is expected)
		if err != nil {
			_, ok := err.(*exec.ExitError)
			assert.True(t, ok, "expected ExitError after signal, got: %v", err)
		}
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		t.Fatal("upgrade did not exit within 10 seconds after SIGTERM")
	}
}

func TestUpgrade_MissingInstallPathFails(t *testing.T) {
	baseDir := setupUpdateDirs(t)

	// Build the hostlink binary
	binDir := t.TempDir()
	hostlinkBin := buildHostlinkBinary(t, binDir)

	// Run upgrade with a non-existent install-path — backup phase should fail
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, hostlinkBin, "upgrade",
		"--base-dir", baseDir,
		"--install-path", "/nonexistent/path/hostlink",
	)
	cmd.Env = testEnv()
	output, err := cmd.CombinedOutput()
	require.Error(t, err, "upgrade should fail with non-existent install-path")
	assert.Contains(t, string(output), "no such file",
		"error should indicate file not found")
}

func TestUpgrade_DryRun(t *testing.T) {
	baseDir := setupUpdateDirs(t)

	// Build the hostlink binary
	binDir := t.TempDir()
	hostlinkBin := buildHostlinkBinary(t, binDir)

	// Run dry-run with a non-existent install-path — some checks should fail
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, hostlinkBin, "upgrade",
		"--dry-run",
		"--base-dir", baseDir,
		"--install-path", "/nonexistent/path/hostlink",
	)
	cmd.Env = testEnv()
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	// Should exit with error because checks fail
	require.Error(t, err, "dry-run should fail when checks don't pass")

	// Should contain check result output format
	assert.Contains(t, outputStr, "[FAIL]", "should report failed checks")
	assert.Contains(t, outputStr, "binary_writable", "should check binary_writable")
}

func TestUpgrade_DryRunPassesWithValidPath(t *testing.T) {
	baseDir := setupUpdateDirs(t)

	// Build the hostlink binary
	binDir := t.TempDir()
	hostlinkBin := buildHostlinkBinary(t, binDir)

	// Create a writable dummy binary at a temp path
	installDir := t.TempDir()
	installPath := createDummyBinary(t, installDir)

	// Run dry-run with a valid install-path
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, hostlinkBin, "upgrade",
		"--dry-run",
		"--base-dir", baseDir,
		"--install-path", installPath,
	)
	cmd.Env = testEnv()
	output, err := cmd.CombinedOutput()
	outputStr := string(output)

	// Some checks should pass (lock, binary_writable, backup_dir)
	assert.Contains(t, outputStr, "[PASS]", "should report passing checks")
	assert.Contains(t, outputStr, "lock_acquirable", "should check lock_acquirable")
	assert.Contains(t, outputStr, "binary_writable", "should check binary_writable")

	// service_exists will fail on non-hostlink machines, so overall exits with error
	// but the path-related checks should pass
	if err != nil {
		assert.Contains(t, outputStr, "[FAIL]",
			"if error, should be because service_exists or similar check failed")
	}
}

func TestUpgrade_VersionSubcommand(t *testing.T) {
	// Build the hostlink binary
	binDir := t.TempDir()
	hostlinkBin := buildHostlinkBinary(t, binDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, hostlinkBin, "version")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "version subcommand should not fail")
	assert.Contains(t, string(output), "dev", "should print version info")
}
