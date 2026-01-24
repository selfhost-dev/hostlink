package upgrade

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// CheckName identifies a dry-run precondition check.
type CheckName string

const (
	CheckLock      CheckName = "lock_acquirable"
	CheckBinary    CheckName = "binary_writable"
	CheckBackupDir CheckName = "backup_dir_writable"
	CheckSelf      CheckName = "self_executable"
	CheckService   CheckName = "service_exists"
	CheckDiskSpace CheckName = "disk_space"
)

// CheckResult holds the outcome of a single dry-run check.
type CheckResult struct {
	Name   CheckName
	Passed bool
	Detail string // Human-readable detail (error message if failed)
}

// DryRun validates all upgrade preconditions without modifying state.
// Returns a slice of check results (one per check) and an error only if
// something unexpected prevents the checks from running at all.
func (u *Upgrader) DryRun(ctx context.Context) []CheckResult {
	results := make([]CheckResult, 0, 6)

	results = append(results, u.checkLock())
	results = append(results, u.checkBinaryWritable())
	results = append(results, u.checkBackupDir())
	results = append(results, u.checkSelfExecutable())
	results = append(results, u.checkServiceExists(ctx))
	results = append(results, u.checkDiskSpace())

	return results
}

// checkLock verifies the upgrade lock can be acquired (then immediately releases it).
func (u *Upgrader) checkLock() CheckResult {
	err := u.lock.TryLock(DefaultLockExpiration)
	if err != nil {
		return CheckResult{Name: CheckLock, Passed: false, Detail: err.Error()}
	}
	u.lock.Unlock()
	return CheckResult{Name: CheckLock, Passed: true, Detail: "lock is available"}
}

// checkBinaryWritable verifies the install path exists and is writable.
func (u *Upgrader) checkBinaryWritable() CheckResult {
	info, err := os.Stat(u.config.InstallPath)
	if err != nil {
		return CheckResult{Name: CheckBinary, Passed: false, Detail: fmt.Sprintf("cannot stat: %v", err)}
	}
	if info.IsDir() {
		return CheckResult{Name: CheckBinary, Passed: false, Detail: "path is a directory"}
	}

	// Check write access to the directory (needed for atomic rename)
	dir := filepath.Dir(u.config.InstallPath)
	if err := unix.Access(dir, unix.W_OK); err != nil {
		return CheckResult{Name: CheckBinary, Passed: false, Detail: fmt.Sprintf("directory not writable: %v", err)}
	}

	return CheckResult{Name: CheckBinary, Passed: true, Detail: "binary exists and directory is writable"}
}

// checkBackupDir verifies the backup directory is writable or can be created.
func (u *Upgrader) checkBackupDir() CheckResult {
	info, err := os.Stat(u.config.BackupDir)

	// Directory doesn't exist — verify parent is writable (could create it)
	if os.IsNotExist(err) {
		parent := filepath.Dir(u.config.BackupDir)
		if err := unix.Access(parent, unix.W_OK); err != nil {
			return CheckResult{Name: CheckBackupDir, Passed: false, Detail: fmt.Sprintf("parent not writable: %v", err)}
		}
		return CheckResult{Name: CheckBackupDir, Passed: true, Detail: "backup directory can be created"}
	}

	// Stat failed for another reason
	if err != nil {
		return CheckResult{Name: CheckBackupDir, Passed: false, Detail: fmt.Sprintf("cannot stat: %v", err)}
	}

	// Path exists but is not a directory
	if !info.IsDir() {
		return CheckResult{Name: CheckBackupDir, Passed: false, Detail: "path is not a directory"}
	}

	// Directory exists — verify writable
	if err := unix.Access(u.config.BackupDir, unix.W_OK); err != nil {
		return CheckResult{Name: CheckBackupDir, Passed: false, Detail: fmt.Sprintf("not writable: %v", err)}
	}

	return CheckResult{Name: CheckBackupDir, Passed: true, Detail: "backup directory is writable"}
}

// checkSelfExecutable verifies the staged binary (self) exists and is executable.
func (u *Upgrader) checkSelfExecutable() CheckResult {
	info, err := os.Stat(u.config.SelfPath)
	if err != nil {
		return CheckResult{Name: CheckSelf, Passed: false, Detail: fmt.Sprintf("cannot stat: %v", err)}
	}
	if info.IsDir() {
		return CheckResult{Name: CheckSelf, Passed: false, Detail: "path is a directory"}
	}

	if err := unix.Access(u.config.SelfPath, unix.X_OK); err != nil {
		return CheckResult{Name: CheckSelf, Passed: false, Detail: fmt.Sprintf("not executable: %v", err)}
	}

	return CheckResult{Name: CheckSelf, Passed: true, Detail: "staged binary is executable"}
}

// checkServiceExists verifies the systemd service unit is loaded.
func (u *Upgrader) checkServiceExists(ctx context.Context) CheckResult {
	exists, err := u.serviceController.Exists(ctx)
	if err != nil {
		return CheckResult{Name: CheckService, Passed: false, Detail: fmt.Sprintf("check failed: %v", err)}
	}
	if !exists {
		return CheckResult{Name: CheckService, Passed: false, Detail: "service unit not found"}
	}
	return CheckResult{Name: CheckService, Passed: true, Detail: "service unit is loaded"}
}

// checkDiskSpace verifies there is enough space for the backup.
func (u *Upgrader) checkDiskSpace() CheckResult {
	info, err := os.Stat(u.config.InstallPath)
	if err != nil {
		return CheckResult{Name: CheckDiskSpace, Passed: false, Detail: fmt.Sprintf("cannot stat binary: %v", err)}
	}
	binarySize := info.Size()

	// Check available space in backup directory (or its parent if it doesn't exist yet)
	statDir := u.config.BackupDir
	if _, err := os.Stat(statDir); os.IsNotExist(err) {
		statDir = filepath.Dir(statDir)
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(statDir, &stat); err != nil {
		return CheckResult{Name: CheckDiskSpace, Passed: false, Detail: fmt.Sprintf("cannot check disk space: %v", err)}
	}

	available := int64(stat.Bavail) * int64(stat.Bsize)
	// Need at least 2x binary size (backup + new binary during atomic rename)
	required := binarySize * 2
	if available < required {
		return CheckResult{
			Name:   CheckDiskSpace,
			Passed: false,
			Detail: fmt.Sprintf("insufficient space: need %d bytes, have %d", required, available),
		}
	}

	return CheckResult{Name: CheckDiskSpace, Passed: true, Detail: fmt.Sprintf("sufficient space available (%d bytes free)", available)}
}
