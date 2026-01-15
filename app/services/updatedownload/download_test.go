package updatedownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to create a test server that returns given content
func createTestServer(t *testing.T, content []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
}

// Helper to create a server that returns a specific status code
func createStatusServer(t *testing.T, statusCode int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(statusCode)
	}))
}

// Helper to compute SHA256 of content
func computeSHA256(content []byte) string {
	hash := sha256.Sum256(content)
	return hex.EncodeToString(hash[:])
}

// noopSleep is a no-op sleep function for fast tests
func noopSleep(d time.Duration) {}

// ============================================================================
// Download Tests
// ============================================================================

func TestDownload_SuccessOnFirstAttempt(t *testing.T) {
	content := []byte("test file content")
	server := createTestServer(t, content)
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	err := d.Download(context.Background(), server.URL, destPath)

	require.NoError(t, err)

	// Verify file was created with correct content
	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestDownload_RetriesOn5xxErrors(t *testing.T) {
	var attemptCount atomic.Int32
	content := []byte("success after retries")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attemptCount.Add(1)
		if count < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	err := d.Download(context.Background(), server.URL, destPath)

	require.NoError(t, err)
	assert.Equal(t, int32(3), attemptCount.Load(), "should have made 3 attempts")

	// Verify file content
	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestDownload_RetriesOnNetworkTimeout(t *testing.T) {
	var attemptCount atomic.Int32
	content := []byte("success after timeout")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := attemptCount.Add(1)
		if count < 3 {
			// Simulate timeout by not responding (connection will timeout)
			time.Sleep(200 * time.Millisecond)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.bin")

	config := &DownloadConfig{
		MaxRetries:        5,
		InitialBackoff:    1 * time.Millisecond,
		MaxBackoff:        10 * time.Millisecond,
		BackoffMultiplier: 2,
		Timeout:           50 * time.Millisecond, // Short timeout to trigger retries
	}
	d := NewDownloaderWithSleep(config, noopSleep)
	err := d.Download(context.Background(), server.URL, destPath)

	require.NoError(t, err)
	assert.GreaterOrEqual(t, attemptCount.Load(), int32(3), "should have retried on timeout")
}

func TestDownload_NoRetryOn4xxErrors(t *testing.T) {
	var attemptCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	err := d.Download(context.Background(), server.URL, destPath)

	require.Error(t, err)
	assert.Equal(t, int32(1), attemptCount.Load(), "should NOT retry on 4xx errors")
}

func TestDownload_ExponentialBackoff(t *testing.T) {
	var sleepDurations []time.Duration

	server := createStatusServer(t, http.StatusInternalServerError)
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.bin")

	config := &DownloadConfig{
		MaxRetries:        5,
		InitialBackoff:    5 * time.Second,
		MaxBackoff:        60 * time.Second,
		BackoffMultiplier: 2,
		Timeout:           1 * time.Second,
	}

	trackingSleep := func(d time.Duration) {
		sleepDurations = append(sleepDurations, d)
	}

	d := NewDownloaderWithSleep(config, trackingSleep)
	_ = d.Download(context.Background(), server.URL, destPath)

	// Expected backoff: 5s, 10s, 20s, 40s, 60s (capped)
	expected := []time.Duration{
		5 * time.Second,
		10 * time.Second,
		20 * time.Second,
		40 * time.Second,
		60 * time.Second,
	}

	require.Equal(t, len(expected), len(sleepDurations), "should sleep between each retry")
	for i, exp := range expected {
		assert.Equal(t, exp, sleepDurations[i], "backoff at retry %d", i+1)
	}
}

func TestDownload_MaxRetriesExceeded(t *testing.T) {
	var attemptCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.bin")

	config := &DownloadConfig{
		MaxRetries:        3,
		InitialBackoff:    1 * time.Millisecond,
		MaxBackoff:        10 * time.Millisecond,
		BackoffMultiplier: 2,
		Timeout:           1 * time.Second,
	}

	d := NewDownloaderWithSleep(config, noopSleep)
	err := d.Download(context.Background(), server.URL, destPath)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrDownloadFailed)
	assert.Equal(t, int32(4), attemptCount.Load(), "should make 1 initial + 3 retries = 4 attempts")
}

func TestDownload_ContextCancellation(t *testing.T) {
	var attemptCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.bin")

	ctx, cancel := context.WithCancel(context.Background())

	// Cancel after first sleep
	sleepWithCancel := func(d time.Duration) {
		cancel()
	}

	d := NewDownloaderWithSleep(DefaultDownloadConfig(), sleepWithCancel)
	err := d.Download(ctx, server.URL, destPath)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
	assert.LessOrEqual(t, attemptCount.Load(), int32(2), "should stop retrying after context cancelled")
}

func TestDownload_AtomicWrite(t *testing.T) {
	content := []byte("atomic write test content")
	server := createTestServer(t, content)
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "downloaded.bin")

	d := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	err := d.Download(context.Background(), server.URL, destPath)

	require.NoError(t, err)

	// Verify no temp files left behind
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)

	for _, entry := range entries {
		assert.Equal(t, "downloaded.bin", entry.Name(), "only destination file should exist")
	}
}

func TestDownload_NoRetryOnDiskWriteError(t *testing.T) {
	var attemptCount atomic.Int32
	content := []byte("test content")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attemptCount.Add(1)
		w.WriteHeader(http.StatusOK)
		w.Write(content)
	}))
	defer server.Close()

	// Try to download to a read-only directory (will fail on write)
	tmpDir := t.TempDir()
	readOnlyDir := filepath.Join(tmpDir, "readonly")
	err := os.MkdirAll(readOnlyDir, 0555) // read + execute only, no write
	require.NoError(t, err)

	destPath := filepath.Join(readOnlyDir, "downloaded.bin")

	d := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	err = d.Download(context.Background(), server.URL, destPath)

	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrDownloadFailed, "disk errors should not result in ErrDownloadFailed")
	assert.Equal(t, int32(1), attemptCount.Load(), "should NOT retry on disk write errors")
}

// ============================================================================
// VerifySHA256 Tests
// ============================================================================

func TestVerifySHA256_Match(t *testing.T) {
	content := []byte("test content for hashing")
	expectedSHA := computeSHA256(content)

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.bin")
	err := os.WriteFile(filePath, content, 0644)
	require.NoError(t, err)

	err = VerifySHA256(filePath, expectedSHA)
	assert.NoError(t, err)
}

func TestVerifySHA256_Mismatch(t *testing.T) {
	content := []byte("test content for hashing")
	wrongSHA := "0000000000000000000000000000000000000000000000000000000000000000"

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.bin")
	err := os.WriteFile(filePath, content, 0644)
	require.NoError(t, err)

	err = VerifySHA256(filePath, wrongSHA)
	assert.ErrorIs(t, err, ErrChecksumMismatch)
}

func TestVerifySHA256_StreamingLargeFile(t *testing.T) {
	// Create a "large" file (1MB) to ensure streaming works
	size := 1024 * 1024
	content := make([]byte, size)
	for i := range content {
		content[i] = byte(i % 256)
	}
	expectedSHA := computeSHA256(content)

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "large.bin")
	err := os.WriteFile(filePath, content, 0644)
	require.NoError(t, err)

	err = VerifySHA256(filePath, expectedSHA)
	assert.NoError(t, err)
}

// ============================================================================
// DownloadAndVerify Tests
// ============================================================================

func TestDownloadAndVerify_Success(t *testing.T) {
	content := []byte("verified content")
	expectedSHA := computeSHA256(content)

	server := createTestServer(t, content)
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "verified.bin")

	d := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	result, err := d.DownloadAndVerify(context.Background(), server.URL, destPath, expectedSHA)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, destPath, result.FilePath)
	assert.Equal(t, expectedSHA, result.SHA256)

	// Verify file exists with correct content
	data, err := os.ReadFile(destPath)
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestDownloadAndVerify_DeletesOnMismatch(t *testing.T) {
	content := []byte("content with wrong checksum")
	wrongSHA := "0000000000000000000000000000000000000000000000000000000000000000"

	server := createTestServer(t, content)
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "bad.bin")

	d := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	result, err := d.DownloadAndVerify(context.Background(), server.URL, destPath, wrongSHA)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrChecksumMismatch)
	assert.Nil(t, result)

	// File should be deleted on checksum failure
	_, err = os.Stat(destPath)
	assert.True(t, os.IsNotExist(err), "file should be deleted on checksum mismatch")
}

func TestDownloadAndVerify_ReturnsResult(t *testing.T) {
	content := []byte("result test content")
	expectedSHA := computeSHA256(content)

	server := createTestServer(t, content)
	defer server.Close()

	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "result.bin")

	d := NewDownloaderWithSleep(DefaultDownloadConfig(), noopSleep)
	result, err := d.DownloadAndVerify(context.Background(), server.URL, destPath, expectedSHA)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Verify result struct is populated correctly
	assert.Equal(t, destPath, result.FilePath)
	assert.Equal(t, expectedSHA, result.SHA256)
}
