package update

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInitDirectories_CreatesAllDirs(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "updates")

	err := InitDirectories(basePath)
	require.NoError(t, err)

	// Check all required directories exist
	expectedDirs := []string{
		basePath,
		filepath.Join(basePath, "backup"),
		filepath.Join(basePath, "staging"),
		filepath.Join(basePath, "updater"),
	}

	for _, dir := range expectedDirs {
		info, err := os.Stat(dir)
		require.NoError(t, err, "directory should exist: %s", dir)
		assert.True(t, info.IsDir(), "%s should be a directory", dir)
	}
}

func TestInitDirectories_CorrectPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "updates")

	err := InitDirectories(basePath)
	require.NoError(t, err)

	// Check permissions are 0700
	dirs := []string{
		basePath,
		filepath.Join(basePath, "backup"),
		filepath.Join(basePath, "staging"),
		filepath.Join(basePath, "updater"),
	}

	for _, dir := range dirs {
		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0700), info.Mode().Perm(), "directory %s should have 0700 permissions", dir)
	}
}

func TestInitDirectories_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "updates")

	// Call twice - should not error
	err := InitDirectories(basePath)
	require.NoError(t, err)

	err = InitDirectories(basePath)
	require.NoError(t, err)

	// Directories should still exist with correct permissions
	info, err := os.Stat(basePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
}

func TestInitDirectories_CreatesNestedPath(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "var", "lib", "hostlink", "updates")

	err := InitDirectories(basePath)
	require.NoError(t, err)

	// All directories should exist
	_, err = os.Stat(filepath.Join(basePath, "backup"))
	assert.NoError(t, err)
}

func TestInitDirectories_PreservesExistingFiles(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "updates")

	// Create base dir and a file inside
	err := os.MkdirAll(basePath, 0700)
	require.NoError(t, err)

	testFile := filepath.Join(basePath, "state.json")
	err = os.WriteFile(testFile, []byte(`{"test": true}`), 0600)
	require.NoError(t, err)

	// Init should not remove existing files
	err = InitDirectories(basePath)
	require.NoError(t, err)

	// File should still exist
	content, err := os.ReadFile(testFile)
	require.NoError(t, err)
	assert.Equal(t, `{"test": true}`, string(content))
}

func TestDefaultPaths(t *testing.T) {
	paths := DefaultPaths()

	assert.Equal(t, "/var/lib/hostlink/updates", paths.BaseDir)
	assert.Equal(t, "/var/lib/hostlink/updates/backup", paths.BackupDir)
	assert.Equal(t, "/var/lib/hostlink/updates/staging", paths.StagingDir)
	assert.Equal(t, "/var/lib/hostlink/updates/updater", paths.UpdaterDir)
	assert.Equal(t, "/var/lib/hostlink/updates/update.lock", paths.LockFile)
	assert.Equal(t, "/var/lib/hostlink/updates/state.json", paths.StateFile)
}

func TestNewPaths(t *testing.T) {
	paths := NewPaths("/custom/path")

	assert.Equal(t, "/custom/path", paths.BaseDir)
	assert.Equal(t, "/custom/path/backup", paths.BackupDir)
	assert.Equal(t, "/custom/path/staging", paths.StagingDir)
	assert.Equal(t, "/custom/path/updater", paths.UpdaterDir)
	assert.Equal(t, "/custom/path/update.lock", paths.LockFile)
	assert.Equal(t, "/custom/path/state.json", paths.StateFile)
}
