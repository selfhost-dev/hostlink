package update

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupBinary_CopiesFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a source binary
	srcPath := filepath.Join(tmpDir, "hostlink")
	srcContent := []byte("binary content v1.0.0")
	err := os.WriteFile(srcPath, srcContent, 0755)
	require.NoError(t, err)

	// Backup to a subdirectory
	backupDir := filepath.Join(tmpDir, "backup")

	err = BackupBinary(srcPath, backupDir)
	require.NoError(t, err)

	// Verify backup exists with same content
	backupPath := filepath.Join(backupDir, "hostlink")
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	assert.Equal(t, srcContent, backupContent)
}

func TestBackupBinary_CreatesBackupDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "hostlink")
	err := os.WriteFile(srcPath, []byte("content"), 0755)
	require.NoError(t, err)

	// Nested backup directory that doesn't exist
	backupDir := filepath.Join(tmpDir, "deep", "nested", "backup")

	err = BackupBinary(srcPath, backupDir)
	require.NoError(t, err)

	// Verify directory was created
	info, err := os.Stat(backupDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestBackupBinary_OverwritesExistingBackup(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backup")
	err := os.MkdirAll(backupDir, 0755)
	require.NoError(t, err)

	// Create existing backup
	backupPath := filepath.Join(backupDir, "hostlink")
	err = os.WriteFile(backupPath, []byte("old backup"), 0755)
	require.NoError(t, err)

	// Create new source
	srcPath := filepath.Join(tmpDir, "hostlink")
	newContent := []byte("new binary content")
	err = os.WriteFile(srcPath, newContent, 0755)
	require.NoError(t, err)

	err = BackupBinary(srcPath, backupDir)
	require.NoError(t, err)

	// Verify backup was overwritten
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	assert.Equal(t, newContent, backupContent)
}

func TestBackupBinary_PreservesPermissions(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "hostlink")
	err := os.WriteFile(srcPath, []byte("content"), 0755)
	require.NoError(t, err)

	backupDir := filepath.Join(tmpDir, "backup")

	err = BackupBinary(srcPath, backupDir)
	require.NoError(t, err)

	// Verify permissions
	backupPath := filepath.Join(backupDir, "hostlink")
	info, err := os.Stat(backupPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
}

func TestBackupBinary_ReturnsErrorIfSourceMissing(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "nonexistent")
	backupDir := filepath.Join(tmpDir, "backup")

	err := BackupBinary(srcPath, backupDir)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err) || os.IsNotExist(unwrapErr(err)))
}

func TestBackupBinary_Atomic_PreservesExistingOnError(t *testing.T) {
	// Skip on systems where we can't test permission-based failures (e.g., root)
	if os.Getuid() == 0 {
		t.Skip("test requires non-root user to test permission failures")
	}

	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backup")
	err := os.MkdirAll(backupDir, 0755)
	require.NoError(t, err)

	// Create existing backup with known content
	existingBackupContent := []byte("existing backup - must survive")
	backupPath := filepath.Join(backupDir, "hostlink")
	err = os.WriteFile(backupPath, existingBackupContent, 0755)
	require.NoError(t, err)

	// Create a source file, then make the SOURCE DIRECTORY unreadable after opening
	// This simulates a failure that happens AFTER the backup file is created but DURING copy
	// Actually, we need a different approach: use a FIFO or make backup dir unwritable

	// Better approach: make the backup directory read-only AFTER it exists
	// This will cause the backup file creation to succeed (if overwriting) but...
	// Actually no, O_TRUNC on existing file works even if dir is read-only.

	// Simplest reliable test: make source file readable but remove it mid-operation
	// That's racy. Let's use a different approach:
	// Create source as readable, existing backup, then make BACKUP FILE immutable
	// Actually on most systems we can't easily test this.

	// Pragmatic test: verify that with atomic implementation, the temp file pattern is used.
	// We'll test that when copy fails (unreadable source), no partial backup is left.

	// Create a source file that can be opened but fails on read
	// We can do this with a named pipe (FIFO) that we don't write to, causing read to block/fail
	// But that's complex. Let's use a simpler approach:

	// Create source file, make it unreadable AFTER we know BackupBinary will try to read it
	// This is inherently racy, so instead let's verify the behavior we care about:
	// After atomic implementation, the function should use temp files.

	// For now, test with unreadable source - this at least verifies existing backup survives
	// when error happens early (before backup file is touched).
	srcPath := filepath.Join(tmpDir, "unreadable")
	err = os.WriteFile(srcPath, []byte("content"), 0000) // No read permission
	require.NoError(t, err)

	// Attempt backup - should fail
	err = BackupBinary(srcPath, backupDir)
	assert.Error(t, err)

	// Verify existing backup is UNCHANGED
	content, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	assert.Equal(t, existingBackupContent, content, "existing backup should be preserved on error")
}

func TestBackupBinary_Atomic_NoTempFilesOnSuccess(t *testing.T) {
	tmpDir := t.TempDir()

	srcPath := filepath.Join(tmpDir, "hostlink")
	err := os.WriteFile(srcPath, []byte("binary content"), 0755)
	require.NoError(t, err)

	backupDir := filepath.Join(tmpDir, "backup")

	err = BackupBinary(srcPath, backupDir)
	require.NoError(t, err)

	// Verify no temp files left behind in backup directory
	entries, err := os.ReadDir(backupDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp.", "temp file should be cleaned up on success")
	}
	// Should only have the backup file
	assert.Len(t, entries, 1)
	assert.Equal(t, "hostlink", entries[0].Name())
}

func TestBackupBinary_Atomic_UsesAtomicRename(t *testing.T) {
	// This test verifies the atomic write pattern is used by checking that
	// the inode changes after backup (atomic rename creates a new file).
	//
	// With non-atomic code (O_TRUNC), the same file is overwritten, inode unchanged.
	// With atomic code (temp + rename), a new file replaces the old, inode changes.

	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backup")
	err := os.MkdirAll(backupDir, 0755)
	require.NoError(t, err)

	// Create existing backup
	existingContent := []byte("old backup content - v1.0.0")
	backupPath := filepath.Join(backupDir, "hostlink")
	err = os.WriteFile(backupPath, existingContent, 0755)
	require.NoError(t, err)

	// Get inode of existing backup
	existingInfo, err := os.Stat(backupPath)
	require.NoError(t, err)
	existingStat, ok := existingInfo.Sys().(*syscall.Stat_t)
	if !ok {
		t.Skip("cannot get inode on this platform")
	}
	existingInode := existingStat.Ino

	// Create new source
	newContent := []byte("new binary content - v2.0.0 - this is longer than the old one")
	srcPath := filepath.Join(tmpDir, "hostlink")
	err = os.WriteFile(srcPath, newContent, 0755)
	require.NoError(t, err)

	// Perform backup
	err = BackupBinary(srcPath, backupDir)
	require.NoError(t, err)

	// Verify backup has new content
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err)
	assert.Equal(t, newContent, backupContent)

	// With atomic rename, the inode should have changed (new file)
	// With O_TRUNC, the inode would be the same (same file, overwritten)
	newInfo, err := os.Stat(backupPath)
	require.NoError(t, err)
	newStat, ok := newInfo.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	newInode := newStat.Ino

	// Inode should change with atomic rename (new file replaces old)
	assert.NotEqual(t, existingInode, newInode,
		"inode should change with atomic rename; got same inode %d (non-atomic O_TRUNC used)", existingInode)
}

func TestBackupBinary_Atomic_CleansTempOnError(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backup")
	err := os.MkdirAll(backupDir, 0755)
	require.NoError(t, err)

	// Create a source file that will fail during read
	srcPath := filepath.Join(tmpDir, "unreadable")
	err = os.WriteFile(srcPath, []byte("content"), 0000) // No read permission
	require.NoError(t, err)

	// Attempt backup - should fail
	err = BackupBinary(srcPath, backupDir)
	assert.Error(t, err)

	// Verify no temp files left behind
	entries, err := os.ReadDir(backupDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp.", "temp file should be cleaned up on error")
	}
}

func TestInstallBinary_ExtractsAndInstalls(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a tarball with a binary
	tarPath := filepath.Join(tmpDir, "hostlink.tar.gz")
	binaryContent := []byte("new binary v2.0.0")
	createTestTarGz(t, tarPath, "hostlink", binaryContent, 0755)

	destPath := filepath.Join(tmpDir, "installed", "hostlink")

	err := InstallBinary(tarPath, destPath)
	require.NoError(t, err)

	// Verify installed binary
	installedContent, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, binaryContent, installedContent)
}

func TestInstallBinary_SetsPermissions(t *testing.T) {
	tmpDir := t.TempDir()

	tarPath := filepath.Join(tmpDir, "hostlink.tar.gz")
	createTestTarGz(t, tarPath, "hostlink", []byte("binary"), 0755)

	destPath := filepath.Join(tmpDir, "hostlink")

	err := InstallBinary(tarPath, destPath)
	require.NoError(t, err)

	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
}

func TestInstallBinary_AtomicRename(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing binary
	destPath := filepath.Join(tmpDir, "hostlink")
	err := os.WriteFile(destPath, []byte("old binary"), 0755)
	require.NoError(t, err)

	// Create tarball with new binary
	tarPath := filepath.Join(tmpDir, "hostlink.tar.gz")
	newContent := []byte("new binary")
	createTestTarGz(t, tarPath, "hostlink", newContent, 0755)

	err = InstallBinary(tarPath, destPath)
	require.NoError(t, err)

	// Verify new binary is in place
	installedContent, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, newContent, installedContent)

	// Verify no temp files left behind
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp.", "temp file should be cleaned up")
	}
}

func TestInstallBinary_CleansUpTempOnError(t *testing.T) {
	tmpDir := t.TempDir()

	// Create an invalid tarball
	tarPath := filepath.Join(tmpDir, "invalid.tar.gz")
	err := os.WriteFile(tarPath, []byte("not a tarball"), 0644)
	require.NoError(t, err)

	destPath := filepath.Join(tmpDir, "hostlink")

	err = InstallBinary(tarPath, destPath)
	assert.Error(t, err)

	// Verify no temp files left behind
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp.", "temp file should be cleaned up on error")
	}
}

func TestInstallBinary_CreatesDestinationDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	tarPath := filepath.Join(tmpDir, "hostlink.tar.gz")
	createTestTarGz(t, tarPath, "hostlink", []byte("binary"), 0755)

	// Nested destination that doesn't exist
	destPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")

	err := InstallBinary(tarPath, destPath)
	require.NoError(t, err)

	// Verify file exists
	_, err = os.Stat(destPath)
	require.NoError(t, err)
}

func TestRestoreBackup_RestoresFile(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backup")
	err := os.MkdirAll(backupDir, 0755)
	require.NoError(t, err)

	// Create backup
	backupContent := []byte("backup binary v1.0.0")
	backupPath := filepath.Join(backupDir, "hostlink")
	err = os.WriteFile(backupPath, backupContent, 0755)
	require.NoError(t, err)

	// Create destination with different content
	destPath := filepath.Join(tmpDir, "hostlink")
	err = os.WriteFile(destPath, []byte("broken binary"), 0755)
	require.NoError(t, err)

	err = RestoreBackup(backupDir, destPath)
	require.NoError(t, err)

	// Verify restored content
	restoredContent, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, backupContent, restoredContent)
}

func TestRestoreBackup_ReturnsErrorIfBackupMissing(t *testing.T) {
	tmpDir := t.TempDir()

	backupDir := filepath.Join(tmpDir, "backup") // Doesn't exist
	destPath := filepath.Join(tmpDir, "hostlink")

	err := RestoreBackup(backupDir, destPath)
	assert.Error(t, err)
}

func TestRestoreBackup_AtomicReplace(t *testing.T) {
	tmpDir := t.TempDir()
	backupDir := filepath.Join(tmpDir, "backup")
	err := os.MkdirAll(backupDir, 0755)
	require.NoError(t, err)

	backupPath := filepath.Join(backupDir, "hostlink")
	err = os.WriteFile(backupPath, []byte("backup"), 0755)
	require.NoError(t, err)

	destPath := filepath.Join(tmpDir, "hostlink")
	err = os.WriteFile(destPath, []byte("current"), 0755)
	require.NoError(t, err)

	err = RestoreBackup(backupDir, destPath)
	require.NoError(t, err)

	// Verify no temp files left
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.Name() != "backup" && entry.Name() != "hostlink" {
			t.Errorf("unexpected file left behind: %s", entry.Name())
		}
	}
}

func TestInstallBinary_RejectsBinaryExceedingMaxSize(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a tarball with header.Size just over MaxBinarySize (100MB + 1 byte)
	tarPath := filepath.Join(tmpDir, "hostlink.tar.gz")
	var oversizeBytes int64 = MaxBinarySize + 1

	f, err := os.Create(tarPath)
	require.NoError(t, err)

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	// Write header claiming a size of 100MB+1
	err = tw.WriteHeader(&tar.Header{
		Name:     "hostlink",
		Mode:     0755,
		Size:     oversizeBytes,
		Typeflag: tar.TypeReg,
	})
	require.NoError(t, err)

	// Write just a small amount of actual data (header lies about size, but
	// the check should reject based on header.Size before copying)
	smallData := make([]byte, 1024)
	_, err = tw.Write(smallData)
	// tar writer may error because we declared more bytes than we wrote - that's fine
	// Close writers regardless
	tw.Close()
	gw.Close()
	f.Close()

	destPath := filepath.Join(tmpDir, "hostlink")

	err = InstallBinary(tarPath, destPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum allowed size")
}

// Helper function to unwrap errors
func unwrapErr(err error) error {
	for {
		unwrapped := err
		if u, ok := err.(interface{ Unwrap() error }); ok {
			unwrapped = u.Unwrap()
		}
		if unwrapped == err {
			return err
		}
		err = unwrapped
	}
}

func TestInstallSelf_CopiesSelfToDest(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a fake "self" binary (simulating os.Executable())
	selfPath := filepath.Join(tmpDir, "staged-hostlink")
	selfContent := []byte("new binary v3.0.0")
	err := os.WriteFile(selfPath, selfContent, 0755)
	require.NoError(t, err)

	destPath := filepath.Join(tmpDir, "installed", "hostlink")

	err = InstallSelf(selfPath, destPath)
	require.NoError(t, err)

	// Verify installed content
	installedContent, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, selfContent, installedContent)
}

func TestInstallSelf_SetsPermissions(t *testing.T) {
	tmpDir := t.TempDir()

	selfPath := filepath.Join(tmpDir, "staged-hostlink")
	err := os.WriteFile(selfPath, []byte("binary"), 0755)
	require.NoError(t, err)

	destPath := filepath.Join(tmpDir, "hostlink")

	err = InstallSelf(selfPath, destPath)
	require.NoError(t, err)

	info, err := os.Stat(destPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm())
}

func TestInstallSelf_AtomicRename(t *testing.T) {
	tmpDir := t.TempDir()

	// Create existing binary at destination
	destPath := filepath.Join(tmpDir, "hostlink")
	err := os.WriteFile(destPath, []byte("old binary"), 0755)
	require.NoError(t, err)

	selfPath := filepath.Join(tmpDir, "staged-hostlink")
	newContent := []byte("new binary")
	err = os.WriteFile(selfPath, newContent, 0755)
	require.NoError(t, err)

	err = InstallSelf(selfPath, destPath)
	require.NoError(t, err)

	// Verify new binary is in place
	installedContent, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, newContent, installedContent)

	// Verify no temp files left behind
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp.", "temp file should be cleaned up")
	}
}

func TestInstallSelf_CleansUpTempOnError(t *testing.T) {
	tmpDir := t.TempDir()

	// Source doesn't exist
	selfPath := filepath.Join(tmpDir, "nonexistent")
	destPath := filepath.Join(tmpDir, "hostlink")

	err := InstallSelf(selfPath, destPath)
	assert.Error(t, err)

	// Verify no temp files left behind
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp.", "temp file should be cleaned up on error")
	}
}

func TestInstallSelf_CreatesDestinationDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	selfPath := filepath.Join(tmpDir, "staged-hostlink")
	err := os.WriteFile(selfPath, []byte("binary"), 0755)
	require.NoError(t, err)

	// Nested destination that doesn't exist
	destPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")

	err = InstallSelf(selfPath, destPath)
	require.NoError(t, err)

	_, err = os.Stat(destPath)
	require.NoError(t, err)
}

// createTestTarGz creates a tar.gz file containing a single file
func createTestTarGz(t *testing.T, tarPath, filename string, content []byte, mode os.FileMode) {
	t.Helper()

	f, err := os.Create(tarPath)
	require.NoError(t, err)
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	err = tw.WriteHeader(&tar.Header{
		Name: filename,
		Mode: int64(mode),
		Size: int64(len(content)),
	})
	require.NoError(t, err)

	_, err = tw.Write(content)
	require.NoError(t, err)
}
