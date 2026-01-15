// Package updatedownload provides functionality for downloading and verifying update artifacts.
package updatedownload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var (
	// ErrDownloadFailed is returned when download fails after all retries.
	ErrDownloadFailed = errors.New("download failed after retries")
	// ErrChecksumMismatch is returned when SHA256 verification fails.
	ErrChecksumMismatch = errors.New("SHA256 checksum mismatch")
)

// DownloadConfig configures the download behavior.
type DownloadConfig struct {
	MaxRetries        int
	InitialBackoff    time.Duration
	MaxBackoff        time.Duration
	BackoffMultiplier int
	Timeout           time.Duration
}

// DefaultDownloadConfig returns the default download configuration.
// Retries: 5, Backoff: 5s -> 10s -> 20s -> 40s -> 60s (capped)
func DefaultDownloadConfig() *DownloadConfig {
	return &DownloadConfig{
		MaxRetries:        5,
		InitialBackoff:    5 * time.Second,
		MaxBackoff:        60 * time.Second,
		BackoffMultiplier: 2,
		Timeout:           5 * time.Minute,
	}
}

// Downloader downloads files with retry and exponential backoff.
type Downloader struct {
	client    *http.Client
	config    *DownloadConfig
	sleepFunc func(time.Duration)
}

// DownloadResult contains the result of a successful download.
type DownloadResult struct {
	FilePath string
	SHA256   string
}

// NewDownloader creates a new Downloader with the given configuration.
func NewDownloader(config *DownloadConfig) *Downloader {
	if config == nil {
		config = DefaultDownloadConfig()
	}
	return &Downloader{
		client:    &http.Client{Timeout: config.Timeout},
		config:    config,
		sleepFunc: time.Sleep,
	}
}

// NewDownloaderWithSleep creates a Downloader with a custom sleep function for testing.
func NewDownloaderWithSleep(config *DownloadConfig, sleepFunc func(time.Duration)) *Downloader {
	d := NewDownloader(config)
	d.sleepFunc = sleepFunc
	return d
}

// Download downloads a file from url to destPath with retry logic.
// Retries on network errors and 5xx responses.
// Does NOT retry on 4xx errors (returns immediately).
func (d *Downloader) Download(ctx context.Context, url, destPath string) error {
	var lastErr error
	backoff := d.config.InitialBackoff

	for attempt := 0; attempt <= d.config.MaxRetries; attempt++ {
		// Check context before each attempt
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := d.downloadOnce(ctx, url, destPath)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check if error is retryable
		if !d.isRetryable(err) {
			return err
		}

		// Don't sleep after the last attempt
		if attempt < d.config.MaxRetries {
			d.sleepFunc(backoff)
			backoff = d.nextBackoff(backoff)

			// Check context after sleep
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}

	return fmt.Errorf("%w: %v", ErrDownloadFailed, lastErr)
}

// downloadOnce performs a single download attempt.
func (d *Downloader) downloadOnce(ctx context.Context, url, destPath string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return &networkError{err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &httpError{statusCode: resp.StatusCode}
	}

	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write to temp file first (atomic write)
	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), ".download-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Clean up temp file on error
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	// Copy response body to file
	// Use a network-error-tagging reader so we can distinguish network vs disk errors
	_, err = io.Copy(tmpFile, &networkReader{r: resp.Body})
	if err != nil {
		tmpFile.Close()
		// Check if it's a network error (tagged by networkReader) or a disk write error
		var netErr *networkError
		if errors.As(err, &netErr) {
			return err // Already tagged as network error, retryable
		}
		// Disk write error - not retryable
		return fmt.Errorf("failed to write to disk: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	tmpPath = "" // Prevent cleanup since rename succeeded
	return nil
}

// isRetryable returns true if the error should trigger a retry.
func (d *Downloader) isRetryable(err error) bool {
	// Network errors are retryable
	var netErr *networkError
	if errors.As(err, &netErr) {
		return true
	}

	// 5xx errors are retryable, 4xx are not
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return httpErr.statusCode >= 500
	}

	return false
}

// nextBackoff calculates the next backoff duration with exponential increase and cap.
func (d *Downloader) nextBackoff(current time.Duration) time.Duration {
	next := current * time.Duration(d.config.BackoffMultiplier)
	if next > d.config.MaxBackoff {
		return d.config.MaxBackoff
	}
	return next
}

// networkError wraps network-related errors for retry detection.
type networkError struct {
	err error
}

func (e *networkError) Error() string {
	return e.err.Error()
}

func (e *networkError) Unwrap() error {
	return e.err
}

// networkReader wraps an io.Reader and tags any read errors as network errors.
// This allows distinguishing network errors from disk write errors during io.Copy.
type networkReader struct {
	r io.Reader
}

func (nr *networkReader) Read(p []byte) (n int, err error) {
	n, err = nr.r.Read(p)
	if err != nil && err != io.EOF {
		return n, &networkError{err: err}
	}
	return n, err
}

// httpError represents an HTTP error response.
type httpError struct {
	statusCode int
}

func (e *httpError) Error() string {
	return fmt.Sprintf("HTTP %d", e.statusCode)
}

// VerifySHA256 verifies that the file at filePath has the expected SHA256 checksum.
// Returns ErrChecksumMismatch if the checksum doesn't match.
func VerifySHA256(filePath, expectedSHA256 string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("failed to hash file: %w", err)
	}

	actualSHA256 := hex.EncodeToString(h.Sum(nil))
	if actualSHA256 != expectedSHA256 {
		return fmt.Errorf("%w: expected %s, got %s", ErrChecksumMismatch, expectedSHA256, actualSHA256)
	}

	return nil
}

// DownloadAndVerify downloads a file and verifies its SHA256 checksum.
// If verification fails, the downloaded file is deleted.
func (d *Downloader) DownloadAndVerify(ctx context.Context, url, destPath, expectedSHA256 string) (*DownloadResult, error) {
	if err := d.Download(ctx, url, destPath); err != nil {
		return nil, err
	}

	if err := VerifySHA256(destPath, expectedSHA256); err != nil {
		// Delete file on checksum failure
		os.Remove(destPath)
		return nil, err
	}

	return &DownloadResult{
		FilePath: destPath,
		SHA256:   expectedSHA256,
	}, nil
}
