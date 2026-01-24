package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/internal/update"
)

func newDryRunUpgrader(t *testing.T, tmpDir string) (*Upgrader, *mockServiceController) {
	t.Helper()

	installPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	lockPath := filepath.Join(tmpDir, "update.lock")

	// Create the install binary
	createTestBinary(t, installPath, []byte("current binary"))
	// Create the self binary
	createTestBinary(t, selfPath, []byte("staged binary"))

	mockSvc := &mockServiceController{existsVal: true}

	u, err := NewUpgrader(&Config{
		InstallPath:       installPath,
		SelfPath:          selfPath,
		BackupDir:         backupDir,
		LockPath:          lockPath,
		StatePath:         filepath.Join(tmpDir, "state.json"),
		HealthURL:         "http://localhost:8080/health",
		TargetVersion:     "v2.0.0",
		LockRetries:       1,
		LockRetryInterval: 10 * time.Millisecond,
		SleepFunc:         func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = mockSvc

	return u, mockSvc
}

func findCheck(results []CheckResult, name CheckName) *CheckResult {
	for i := range results {
		if results[i].Name == name {
			return &results[i]
		}
	}
	return nil
}

func TestDryRun_AllPassWhenPreConditionsMet(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	results := u.DryRun(context.Background())

	require.Len(t, results, 6)
	for _, r := range results {
		assert.True(t, r.Passed, "check %s should pass: %s", r.Name, r.Detail)
	}
}

func TestDryRun_LockCheck_FailsWhenLocked(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	// Hold the lock
	lockPath := filepath.Join(tmpDir, "update.lock")
	otherLock := update.NewLockManager(update.LockConfig{LockPath: lockPath})
	require.NoError(t, otherLock.TryLock(1*time.Hour))
	defer otherLock.Unlock()

	results := u.DryRun(context.Background())

	check := findCheck(results, CheckLock)
	require.NotNil(t, check)
	assert.False(t, check.Passed)
}

func TestDryRun_LockCheck_ReleasesAfterCheck(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	results := u.DryRun(context.Background())

	// Lock should pass
	check := findCheck(results, CheckLock)
	require.NotNil(t, check)
	assert.True(t, check.Passed)

	// Lock should be released - try again
	lockPath := filepath.Join(tmpDir, "update.lock")
	otherLock := update.NewLockManager(update.LockConfig{LockPath: lockPath})
	err := otherLock.TryLock(1 * time.Hour)
	assert.NoError(t, err, "lock should have been released after dry-run check")
	otherLock.Unlock()
}

func TestDryRun_BinaryCheck_FailsWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	// Remove the install binary
	os.Remove(u.config.InstallPath)

	results := u.DryRun(context.Background())

	check := findCheck(results, CheckBinary)
	require.NotNil(t, check)
	assert.False(t, check.Passed)
	assert.Contains(t, check.Detail, "cannot stat")
}

func TestDryRun_BinaryCheck_FailsWhenDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	// Replace binary with a directory
	os.Remove(u.config.InstallPath)
	os.MkdirAll(u.config.InstallPath, 0755)

	results := u.DryRun(context.Background())

	check := findCheck(results, CheckBinary)
	require.NotNil(t, check)
	assert.False(t, check.Passed)
	assert.Contains(t, check.Detail, "directory")
}

func TestDryRun_BackupDirCheck_PassesWhenParentWritable(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	// Backup dir doesn't exist yet
	_, err := os.Stat(u.config.BackupDir)
	require.True(t, os.IsNotExist(err))

	results := u.DryRun(context.Background())

	check := findCheck(results, CheckBackupDir)
	require.NotNil(t, check)
	assert.True(t, check.Passed)
	assert.Contains(t, check.Detail, "can be created")

	// Directory should NOT have been created (no side effects)
	_, err = os.Stat(u.config.BackupDir)
	assert.True(t, os.IsNotExist(err), "dry-run should not create backup directory")
}

func TestDryRun_SelfCheck_FailsWhenMissing(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	os.Remove(u.config.SelfPath)

	results := u.DryRun(context.Background())

	check := findCheck(results, CheckSelf)
	require.NotNil(t, check)
	assert.False(t, check.Passed)
	assert.Contains(t, check.Detail, "cannot stat")
}

func TestDryRun_SelfCheck_FailsWhenNotExecutable(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	// Remove execute permission
	os.Chmod(u.config.SelfPath, 0644)

	results := u.DryRun(context.Background())

	check := findCheck(results, CheckSelf)
	require.NotNil(t, check)
	assert.False(t, check.Passed)
	assert.Contains(t, check.Detail, "not executable")
}

func TestDryRun_ServiceCheck_FailsWhenNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	u, mockSvc := newDryRunUpgrader(t, tmpDir)

	mockSvc.existsVal = false

	results := u.DryRun(context.Background())

	check := findCheck(results, CheckService)
	require.NotNil(t, check)
	assert.False(t, check.Passed)
	assert.Contains(t, check.Detail, "not found")
}

func TestDryRun_ServiceCheck_FailsOnError(t *testing.T) {
	tmpDir := t.TempDir()
	u, mockSvc := newDryRunUpgrader(t, tmpDir)

	mockSvc.existsVal = false
	mockSvc.existsErr = errors.New("systemctl not available")

	results := u.DryRun(context.Background())

	check := findCheck(results, CheckService)
	require.NotNil(t, check)
	assert.False(t, check.Passed)
	assert.Contains(t, check.Detail, "check failed")
}

func TestDryRun_DiskSpaceCheck_Passes(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	// Ensure backup dir exists for statfs
	os.MkdirAll(u.config.BackupDir, 0755)

	results := u.DryRun(context.Background())

	check := findCheck(results, CheckDiskSpace)
	require.NotNil(t, check)
	assert.True(t, check.Passed)
	assert.Contains(t, check.Detail, "sufficient space")
}

func TestDryRun_DoesNotModifyBinary(t *testing.T) {
	tmpDir := t.TempDir()
	u, _ := newDryRunUpgrader(t, tmpDir)

	originalContent, err := os.ReadFile(u.config.InstallPath)
	require.NoError(t, err)

	u.DryRun(context.Background())

	afterContent, err := os.ReadFile(u.config.InstallPath)
	require.NoError(t, err)
	assert.Equal(t, originalContent, afterContent, "dry-run should not modify the binary")
}

func TestDryRun_DoesNotStopService(t *testing.T) {
	tmpDir := t.TempDir()
	u, mockSvc := newDryRunUpgrader(t, tmpDir)

	u.DryRun(context.Background())

	assert.False(t, mockSvc.stopCalled, "dry-run should not stop the service")
	assert.False(t, mockSvc.startCalled, "dry-run should not start the service")
}

func TestDryRun_ReturnsAllChecksEvenOnFailure(t *testing.T) {
	tmpDir := t.TempDir()
	u, mockSvc := newDryRunUpgrader(t, tmpDir)

	// Make multiple checks fail
	os.Remove(u.config.InstallPath)
	os.Remove(u.config.SelfPath)
	mockSvc.existsVal = false

	results := u.DryRun(context.Background())

	// Should still get all 6 checks
	require.Len(t, results, 6)

	// Verify failed ones
	assert.False(t, findCheck(results, CheckBinary).Passed)
	assert.False(t, findCheck(results, CheckSelf).Passed)
	assert.False(t, findCheck(results, CheckService).Passed)

	// Lock and backup dir should still pass
	assert.True(t, findCheck(results, CheckLock).Passed)
	assert.True(t, findCheck(results, CheckBackupDir).Passed)
}
