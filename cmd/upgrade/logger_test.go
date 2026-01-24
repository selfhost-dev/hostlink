package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/internal/update"
)

func TestNewLogger_CreatesLogFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "sub", "upgrade.log")

	logger, cleanup, err := NewLogger(logPath)
	require.NoError(t, err)
	defer cleanup()

	logger.Info("test message")

	// Verify file was created
	_, err = os.Stat(logPath)
	assert.NoError(t, err)
}

func TestNewLogger_AppendsToExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "upgrade.log")

	// Write existing content
	require.NoError(t, os.WriteFile(logPath, []byte("existing\n"), 0644))

	logger, cleanup, err := NewLogger(logPath)
	require.NoError(t, err)
	defer cleanup()

	logger.Info("new message")

	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(string(content), "existing\n"), "should preserve existing content")
	assert.Contains(t, string(content), "new message")
}

func TestNewLogger_WritesJSON(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "upgrade.log")

	logger, cleanup, err := NewLogger(logPath)
	require.NoError(t, err)
	defer cleanup()

	logger.Info("structured log", "key", "value")

	content, err := os.ReadFile(logPath)
	require.NoError(t, err)

	// Parse as JSON
	var entry map[string]interface{}
	err = json.Unmarshal(content, &entry)
	require.NoError(t, err, "log entry should be valid JSON")
	assert.Equal(t, "structured log", entry["msg"])
	assert.Equal(t, "value", entry["key"])
	assert.Equal(t, "INFO", entry["level"])
}

func TestNewLogger_ErrorsOnInvalidPath(t *testing.T) {
	// Path under a file (not a directory)
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "file")
	require.NoError(t, os.WriteFile(filePath, []byte("x"), 0644))

	logPath := filepath.Join(filePath, "sub", "upgrade.log")

	_, _, err := NewLogger(logPath)
	assert.Error(t, err)
}

func testLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})
	return slog.New(handler), buf
}

func TestUpgrader_Run_LogsPhaseTransitions(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")

	createTestBinary(t, installPath, []byte("old binary"))
	createTestBinary(t, selfPath, []byte("new binary v2.0.0"))

	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: true, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	logger, buf := testLogger(t)

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           filepath.Join(tmpDir, "backup"),
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           filepath.Join(tmpDir, "state.json"),
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		Logger:              logger,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{existsVal: true}

	err = u.Run(context.Background())
	require.NoError(t, err)

	output := buf.String()
	assert.Contains(t, output, "upgrade started")
	assert.Contains(t, output, "lock acquired")
	assert.Contains(t, output, "backup created")
	assert.Contains(t, output, "service stopped")
	assert.Contains(t, output, "binary installed")
	assert.Contains(t, output, "service started")
	assert.Contains(t, output, "health check passed")
	assert.Contains(t, output, "upgrade completed successfully")
}

func TestUpgrader_Run_LogsRollbackOnHealthFailure(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")

	createTestBinary(t, installPath, []byte("old binary"))
	createTestBinary(t, selfPath, []byte("new binary v2.0.0"))

	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: false, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	logger, buf := testLogger(t)

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           filepath.Join(tmpDir, "backup"),
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           filepath.Join(tmpDir, "state.json"),
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		Logger:              logger,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{existsVal: true}

	err = u.Run(context.Background())
	assert.Error(t, err)

	output := buf.String()
	assert.Contains(t, output, "health check failed, rolling back")
	assert.Contains(t, output, "rollback initiated")
	assert.Contains(t, output, "backup restored")
	assert.Contains(t, output, "service restarted after rollback")
	assert.Contains(t, output, "rollback completed")
}

func TestUpgrader_Run_LogsLockFailure(t *testing.T) {
	tmpDir := t.TempDir()

	lockPath := filepath.Join(tmpDir, "update.lock")
	otherLock := update.NewLockManager(update.LockConfig{LockPath: lockPath})
	require.NoError(t, otherLock.TryLock(1*time.Hour))
	defer otherLock.Unlock()

	logger, buf := testLogger(t)

	u, err := NewUpgrader(&Config{
		InstallPath:       filepath.Join(tmpDir, "hostlink"),
		SelfPath:          filepath.Join(tmpDir, "staging", "hostlink"),
		BackupDir:         filepath.Join(tmpDir, "backup"),
		LockPath:          lockPath,
		StatePath:         filepath.Join(tmpDir, "state.json"),
		HealthURL:         "http://localhost:8080/health",
		TargetVersion:     "v2.0.0",
		LockRetries:       1,
		LockRetryInterval: 10 * time.Millisecond,
		Logger:            logger,
		SleepFunc:         func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)

	err = u.Run(context.Background())
	assert.Error(t, err)

	output := buf.String()
	assert.Contains(t, output, "failed to acquire lock")
}

func TestUpgrader_Run_LogsCancellation(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	createTestBinary(t, installPath, []byte("binary"))
	createTestBinary(t, selfPath, []byte("new"))

	logger, buf := testLogger(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Already cancelled

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           filepath.Join(tmpDir, "backup"),
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           filepath.Join(tmpDir, "state.json"),
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		Logger:              logger,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{existsVal: true}

	err = u.Run(ctx)
	assert.ErrorIs(t, err, context.Canceled)

	output := buf.String()
	assert.Contains(t, output, "cancelled")
}

func TestDiscardLogger_DoesNotPanic(t *testing.T) {
	logger := discardLogger()
	// Should not panic
	logger.Info("message", "key", "value")
	logger.Error("error", "err", "something")
}
