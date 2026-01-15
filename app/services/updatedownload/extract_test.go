package updatedownload

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to create a .tar.gz archive for testing
func createTestTarGz(t *testing.T, destPath string, files map[string]testFile) {
	t.Helper()

	f, err := os.Create(destPath)
	require.NoError(t, err)
	defer f.Close()

	gw := gzip.NewWriter(f)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	for name, tf := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: tf.mode,
			Size: int64(len(tf.content)),
		}
		err := tw.WriteHeader(hdr)
		require.NoError(t, err)

		_, err = tw.Write([]byte(tf.content))
		require.NoError(t, err)
	}
}

type testFile struct {
	content string
	mode    int64
}

// ============================================================================
// ExtractTarGz Tests
// ============================================================================

func TestExtractTarGz_ValidArchive(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destDir := filepath.Join(tmpDir, "extracted")

	files := map[string]testFile{
		"file1.txt":        {content: "content of file 1", mode: 0644},
		"file2.txt":        {content: "content of file 2", mode: 0644},
		"subdir/file3.txt": {content: "content in subdir", mode: 0644},
	}
	createTestTarGz(t, tarPath, files)

	err := ExtractTarGz(tarPath, destDir)
	require.NoError(t, err)

	// Verify all files were extracted
	for name, tf := range files {
		path := filepath.Join(destDir, name)
		content, err := os.ReadFile(path)
		require.NoError(t, err, "file %s should exist", name)
		assert.Equal(t, tf.content, string(content), "content of %s", name)
	}
}

func TestExtractTarGz_PreservesPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destDir := filepath.Join(tmpDir, "extracted")

	files := map[string]testFile{
		"readonly.txt":  {content: "readonly", mode: 0444},
		"executable.sh": {content: "#!/bin/bash", mode: 0755},
		"normal.txt":    {content: "normal", mode: 0644},
	}
	createTestTarGz(t, tarPath, files)

	err := ExtractTarGz(tarPath, destDir)
	require.NoError(t, err)

	// Verify permissions
	for name, tf := range files {
		path := filepath.Join(destDir, name)
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(tf.mode), info.Mode().Perm(), "permissions of %s", name)
	}
}

func TestExtractTarGz_CreatesDestDir(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destDir := filepath.Join(tmpDir, "nested", "dest", "dir")

	files := map[string]testFile{
		"file.txt": {content: "test", mode: 0644},
	}
	createTestTarGz(t, tarPath, files)

	// destDir doesn't exist yet
	_, err := os.Stat(destDir)
	require.True(t, os.IsNotExist(err))

	err = ExtractTarGz(tarPath, destDir)
	require.NoError(t, err)

	// destDir should now exist
	info, err := os.Stat(destDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestExtractTarGz_DestDirPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destDir := filepath.Join(tmpDir, "extracted")

	files := map[string]testFile{
		"file.txt": {content: "test", mode: 0644},
	}
	createTestTarGz(t, tarPath, files)

	err := ExtractTarGz(tarPath, destDir)
	require.NoError(t, err)

	// destDir should have 0755 permissions
	info, err := os.Stat(destDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm(), "destDir should have 0755 permissions")
}

func TestExtractTarGz_DestDirCreatedBeforeExtraction(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destDir := filepath.Join(tmpDir, "extracted")

	// Create archive with files in subdirectory only (not directly in destDir)
	files := map[string]testFile{
		"subdir/file.txt": {content: "test", mode: 0644},
	}
	createTestTarGz(t, tarPath, files)

	err := ExtractTarGz(tarPath, destDir)
	require.NoError(t, err)

	// destDir should exist and have correct permissions
	info, err := os.Stat(destDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm(), "destDir should have 0755 permissions")
}

func TestExtractTarGz_FixesExistingDestDirPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destDir := filepath.Join(tmpDir, "extracted")

	files := map[string]testFile{
		"file.txt": {content: "test", mode: 0644},
	}
	createTestTarGz(t, tarPath, files)

	// Create destDir with restrictive permissions
	err := os.MkdirAll(destDir, 0700)
	require.NoError(t, err)

	err = ExtractTarGz(tarPath, destDir)
	require.NoError(t, err)

	// destDir should now have 0755 permissions (fixed)
	info, err := os.Stat(destDir)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0755), info.Mode().Perm(), "destDir permissions should be fixed to 0755")
}

func TestExtractTarGz_InvalidArchive(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "invalid.tar.gz")
	destDir := filepath.Join(tmpDir, "extracted")

	// Write invalid content
	err := os.WriteFile(tarPath, []byte("not a valid tar.gz"), 0644)
	require.NoError(t, err)

	err = ExtractTarGz(tarPath, destDir)
	assert.Error(t, err)
}

func TestExtractTarGz_PathTraversal(t *testing.T) {
	testCases := []struct {
		name     string
		fileName string
	}{
		{"unix style", "../../../etc/passwd"},
		{"windows style backslash", "..\\..\\..\\etc\\passwd"},
		{"mixed slashes", "..\\../..\\etc/passwd"},
		{"hidden in middle", "foo/../../../etc/passwd"},
		{"absolute unix", "/etc/passwd"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tarPath := filepath.Join(tmpDir, "malicious.tar.gz")
			destDir := filepath.Join(tmpDir, "extracted")

			// Create archive with path traversal attempt
			f, err := os.Create(tarPath)
			require.NoError(t, err)

			gw := gzip.NewWriter(f)
			tw := tar.NewWriter(gw)

			// Malicious file with path traversal
			hdr := &tar.Header{
				Name: tc.fileName,
				Mode: 0644,
				Size: int64(len("malicious")),
			}
			err = tw.WriteHeader(hdr)
			require.NoError(t, err)
			_, err = tw.Write([]byte("malicious"))
			require.NoError(t, err)

			tw.Close()
			gw.Close()
			f.Close()

			err = ExtractTarGz(tarPath, destDir)
			assert.Error(t, err, "should reject path traversal: %s", tc.fileName)
			assert.ErrorIs(t, err, ErrPathTraversal)
		})
	}
}

// ============================================================================
// ExtractFile Tests
// ============================================================================

func TestExtractFile_SingleFile(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destPath := filepath.Join(tmpDir, "extracted.txt")

	files := map[string]testFile{
		"file1.txt": {content: "content 1", mode: 0644},
		"file2.txt": {content: "content 2", mode: 0644},
		"file3.txt": {content: "content 3", mode: 0644},
	}
	createTestTarGz(t, tarPath, files)

	err := ExtractFile(tarPath, "file2.txt", destPath)
	require.NoError(t, err)

	content, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, "content 2", string(content))
}

func TestExtractFile_PreservesPermissions(t *testing.T) {
	testCases := []struct {
		name string
		mode int64
	}{
		{"executable", 0755},
		{"readonly", 0444},
		{"normal", 0644},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			tarPath := filepath.Join(tmpDir, "test.tar.gz")
			destPath := filepath.Join(tmpDir, "extracted")

			files := map[string]testFile{
				"file.bin": {content: "binary content", mode: tc.mode},
			}
			createTestTarGz(t, tarPath, files)

			err := ExtractFile(tarPath, "file.bin", destPath)
			require.NoError(t, err)

			info, err := os.Stat(destPath)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(tc.mode), info.Mode().Perm(), "should preserve %s permissions", tc.name)
		})
	}
}

func TestExtractFile_NotFound(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destPath := filepath.Join(tmpDir, "extracted.txt")

	files := map[string]testFile{
		"file1.txt": {content: "content 1", mode: 0644},
	}
	createTestTarGz(t, tarPath, files)

	err := ExtractFile(tarPath, "nonexistent.txt", destPath)
	assert.Error(t, err, "should error when file not found in archive")
}

func TestExtractFile_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destPath := filepath.Join(tmpDir, "extracted.txt")

	files := map[string]testFile{
		"file.txt": {content: "atomic content", mode: 0644},
	}
	createTestTarGz(t, tarPath, files)

	err := ExtractFile(tarPath, "file.txt", destPath)
	require.NoError(t, err)

	// Verify no temp files left behind
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)

	fileNames := make([]string, 0, len(entries))
	for _, entry := range entries {
		fileNames = append(fileNames, entry.Name())
	}

	assert.Contains(t, fileNames, "test.tar.gz")
	assert.Contains(t, fileNames, "extracted.txt")
	assert.Len(t, fileNames, 2, "should only have tar and extracted file, no temp files")
}
