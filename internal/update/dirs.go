package update

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// DefaultBaseDir is the default base directory for update files.
	DefaultBaseDir = "/var/lib/hostlink/updates"

	// DirPermissions is the permission mode for update directories (owner rwx only).
	DirPermissions = 0700
)

// Paths holds all the paths used by the update system.
type Paths struct {
	BaseDir    string // /var/lib/hostlink/updates
	BackupDir  string // /var/lib/hostlink/updates/backup
	StagingDir string // /var/lib/hostlink/updates/staging
	UpdaterDir string // /var/lib/hostlink/updates/updater
	LockFile   string // /var/lib/hostlink/updates/update.lock
	StateFile  string // /var/lib/hostlink/updates/state.json
}

// DefaultPaths returns the default paths for the update system.
func DefaultPaths() Paths {
	return NewPaths(DefaultBaseDir)
}

// NewPaths creates a Paths struct with the given base directory.
func NewPaths(baseDir string) Paths {
	return Paths{
		BaseDir:    baseDir,
		BackupDir:  filepath.Join(baseDir, "backup"),
		StagingDir: filepath.Join(baseDir, "staging"),
		UpdaterDir: filepath.Join(baseDir, "updater"),
		LockFile:   filepath.Join(baseDir, "update.lock"),
		StateFile:  filepath.Join(baseDir, "state.json"),
	}
}

// InitDirectories creates all required directories for the update system
// with correct permissions (0700). This function is idempotent.
func InitDirectories(baseDir string) error {
	paths := NewPaths(baseDir)

	dirs := []string{
		paths.BaseDir,
		paths.BackupDir,
		paths.StagingDir,
		paths.UpdaterDir,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, DirPermissions); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}

		// Ensure permissions are correct even if directory already exists
		// (MkdirAll doesn't change permissions of existing directories)
		if err := os.Chmod(dir, DirPermissions); err != nil {
			return fmt.Errorf("failed to set permissions on %s: %w", dir, err)
		}
	}

	return nil
}
