package updatedownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStagingManager_Prepare(t *testing.T) {
	tmpDir := t.TempDir()
	stagingPath := filepath.Join(tmpDir, "staging")

	// Staging dir doesn't exist yet
	_, err := os.Stat(stagingPath)
	require.True(t, os.IsNotExist(err))

	sm := NewStagingManager(stagingPath, nil)
	err = sm.Prepare()
	require.NoError(t, err)

	// Staging dir should now exist with 0700 permissions
	info, err := os.Stat(stagingPath)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
}

func TestStagingManager_Prepare_Idempotent(t *testing.T) {
	tmpDir := t.TempDir()
	stagingPath := filepath.Join(tmpDir, "staging")

	sm := NewStagingManager(stagingPath, nil)

	// Call Prepare twice - should not error
	err := sm.Prepare()
	require.NoError(t, err)

	err = sm.Prepare()
	require.NoError(t, err)

	// Should still have correct permissions
	info, err := os.Stat(stagingPath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0700), info.Mode().Perm())
}

func TestStagingManager_StageAgent(t *testing.T) {
	content := []byte("fake agent tarball content")
	contentSHA := computeStagingSHA256(content)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	stagingPath := filepath.Join(tmpDir, "staging")

	downloader := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	sm := NewStagingManager(stagingPath, downloader)

	err := sm.Prepare()
	require.NoError(t, err)

	err = sm.StageAgent(context.Background(), server.URL, contentSHA)
	require.NoError(t, err)

	// Verify file was downloaded
	agentPath := sm.GetAgentPath()
	data, err := os.ReadFile(agentPath)
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestStagingManager_GetAgentPath(t *testing.T) {
	stagingPath := "/var/lib/hostlink/updates/staging"
	sm := NewStagingManager(stagingPath, nil)

	assert.Equal(t, filepath.Join(stagingPath, "hostlink.tar.gz"), sm.GetAgentPath())
}

func TestStagingManager_Cleanup(t *testing.T) {
	tmpDir := t.TempDir()
	stagingPath := filepath.Join(tmpDir, "staging")

	sm := NewStagingManager(stagingPath, nil)

	err := sm.Prepare()
	require.NoError(t, err)

	// Create some files in staging
	file1 := filepath.Join(stagingPath, "file1.txt")
	file2 := filepath.Join(stagingPath, "file2.txt")
	err = os.WriteFile(file1, []byte("content1"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(file2, []byte("content2"), 0644)
	require.NoError(t, err)

	// Cleanup
	err = sm.Cleanup()
	require.NoError(t, err)

	// Staging directory should be completely removed
	_, err = os.Stat(stagingPath)
	assert.True(t, os.IsNotExist(err), "staging directory should be removed after cleanup")
}

func TestStagingManager_Cleanup_NonExistentDir(t *testing.T) {
	tmpDir := t.TempDir()
	stagingPath := filepath.Join(tmpDir, "nonexistent")

	sm := NewStagingManager(stagingPath, nil)

	// Cleanup on non-existent dir should not error
	err := sm.Cleanup()
	assert.NoError(t, err)
}

func TestStagingManager_StageAgent_ChecksumMismatch(t *testing.T) {
	content := []byte("agent content")
	wrongSHA := "0000000000000000000000000000000000000000000000000000000000000000"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	stagingPath := filepath.Join(tmpDir, "staging")

	downloader := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	sm := NewStagingManager(stagingPath, downloader)

	err := sm.Prepare()
	require.NoError(t, err)

	err = sm.StageAgent(context.Background(), server.URL, wrongSHA)
	assert.ErrorIs(t, err, ErrChecksumMismatch)

	// File should not exist after checksum failure
	agentPath := sm.GetAgentPath()
	_, err = os.Stat(agentPath)
	assert.True(t, os.IsNotExist(err), "file should be deleted on checksum mismatch")
}

// Helper for staging tests
func computeStagingSHA256(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}
