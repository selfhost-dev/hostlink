package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/internal/update"
)

// Test helpers
func createTestBinary(t *testing.T, path string, content []byte) {
	t.Helper()
	dir := filepath.Dir(path)
	require.NoError(t, os.MkdirAll(dir, 0755))
	require.NoError(t, os.WriteFile(path, content, 0755))
}

func createTestTarball(t *testing.T, path string, binaryContent []byte) {
	t.Helper()
	dir := filepath.Dir(path)
	require.NoError(t, os.MkdirAll(dir, 0755))

	// Create a tar.gz file using archive/tar and compress/gzip
	f, err := os.Create(path)
	require.NoError(t, err)
	defer f.Close()

	gw := newGzipWriter(f)
	defer gw.Close()

	tw := newTarWriter(gw)
	defer tw.Close()

	require.NoError(t, tw.WriteHeader(&tarHeader{
		Name: "hostlink",
		Mode: 0755,
		Size: int64(len(binaryContent)),
	}))
	_, err = tw.Write(binaryContent)
	require.NoError(t, err)
}

func TestUpdater_Run_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Setup paths
	binaryPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	stagingDir := filepath.Join(tmpDir, "staging")
	lockPath := filepath.Join(tmpDir, "update.lock")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create current binary
	createTestBinary(t, binaryPath, []byte("old binary v1.0.0"))

	// Create staged tarball with new binary
	tarballPath := filepath.Join(stagingDir, "hostlink.tar.gz")
	createTestTarball(t, tarballPath, []byte("new binary v2.0.0"))

	// Mock health server
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: true, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	// Create updater
	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           backupDir,
		StagingDir:          stagingDir,
		LockPath:            lockPath,
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(d time.Duration) {}, // No-op for tests
	})

	// Mock service controller (no real systemctl)
	u.serviceController = &mockServiceController{}

	err := u.Run(context.Background())
	require.NoError(t, err)

	// Verify new binary is installed
	content, err := os.ReadFile(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("new binary v2.0.0"), content)

	// Verify backup exists
	backupContent, err := os.ReadFile(filepath.Join(backupDir, "hostlink"))
	require.NoError(t, err)
	assert.Equal(t, []byte("old binary v1.0.0"), backupContent)
}

func TestUpdater_Run_RollbackOnHealthCheckFailure(t *testing.T) {
	tmpDir := t.TempDir()

	// Setup paths
	binaryPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	stagingDir := filepath.Join(tmpDir, "staging")
	lockPath := filepath.Join(tmpDir, "update.lock")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create current binary
	oldContent := []byte("old binary v1.0.0")
	createTestBinary(t, binaryPath, oldContent)

	// Create staged tarball with new binary
	tarballPath := filepath.Join(stagingDir, "hostlink.tar.gz")
	createTestTarball(t, tarballPath, []byte("new binary v2.0.0"))

	// Mock health server that always returns unhealthy
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: false, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           backupDir,
		StagingDir:          stagingDir,
		LockPath:            lockPath,
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(d time.Duration) {},
	})

	u.serviceController = &mockServiceController{}

	err := u.Run(context.Background())
	assert.Error(t, err)

	// Verify rollback occurred - old binary should be restored
	content, err := os.ReadFile(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, oldContent, content)
}

func TestUpdater_Run_LockAcquisitionFailure(t *testing.T) {
	tmpDir := t.TempDir()

	lockPath := filepath.Join(tmpDir, "update.lock")

	// Acquire lock first with another lock manager to simulate contention
	otherLock := update.NewLockManager(update.LockConfig{LockPath: lockPath})
	require.NoError(t, otherLock.TryLock(1*time.Hour))
	defer otherLock.Unlock()

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:   filepath.Join(tmpDir, "hostlink"),
		BackupDir:         filepath.Join(tmpDir, "backup"),
		StagingDir:        filepath.Join(tmpDir, "staging"),
		LockPath:          lockPath,
		StatePath:         filepath.Join(tmpDir, "state.json"),
		HealthURL:         "http://localhost:8080/health",
		TargetVersion:     "v2.0.0",
		LockRetries:       1,
		LockRetryInterval: 10 * time.Millisecond,
	})

	err := u.Run(context.Background())
	assert.Error(t, err)
	assert.ErrorIs(t, err, update.ErrLockAcquireFailed)
}

func TestUpdater_Run_CleansUpTempFiles(t *testing.T) {
	tmpDir := t.TempDir()

	binaryPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	stagingDir := filepath.Join(tmpDir, "staging")
	lockPath := filepath.Join(tmpDir, "update.lock")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create current binary
	createTestBinary(t, binaryPath, []byte("old binary"))

	// Create leftover temp files
	tempFile1 := filepath.Join(tmpDir, "usr", "bin", "hostlink.tmp.abc123")
	tempFile2 := filepath.Join(tmpDir, "usr", "bin", "hostlink.tmp.def456")
	require.NoError(t, os.WriteFile(tempFile1, []byte("temp"), 0755))
	require.NoError(t, os.WriteFile(tempFile2, []byte("temp"), 0755))

	// Create staged tarball
	tarballPath := filepath.Join(stagingDir, "hostlink.tar.gz")
	createTestTarball(t, tarballPath, []byte("new binary v2.0.0"))

	// Mock health server
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: true, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           backupDir,
		StagingDir:          stagingDir,
		LockPath:            lockPath,
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(d time.Duration) {},
	})

	u.serviceController = &mockServiceController{}

	err := u.Run(context.Background())
	require.NoError(t, err)

	// Verify temp files were cleaned up
	entries, err := os.ReadDir(filepath.Dir(binaryPath))
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp.", "temp files should be cleaned up")
	}
}

func TestUpdater_Rollback_RestoresAndStartsService(t *testing.T) {
	tmpDir := t.TempDir()

	binaryPath := filepath.Join(tmpDir, "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create backup
	backupContent := []byte("backup binary v1.0.0")
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "hostlink"), backupContent, 0755))

	// Create "broken" current binary
	require.NoError(t, os.WriteFile(binaryPath, []byte("broken"), 0755))

	// Track service call order
	var callOrder []string
	mockSvc := &mockServiceController{
		onStop:  func() { callOrder = append(callOrder, "stop") },
		onStart: func() { callOrder = append(callOrder, "start") },
	}

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           backupDir,
		StagingDir:          filepath.Join(tmpDir, "staging"),
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
	})
	u.serviceController = mockSvc

	err := u.Rollback(context.Background())
	require.NoError(t, err)

	// Verify binary was restored
	content, err := os.ReadFile(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, backupContent, content)

	// Verify service was stopped then started (in that order)
	assert.True(t, mockSvc.stopCalled)
	assert.True(t, mockSvc.startCalled)
	assert.Equal(t, []string{"stop", "start"}, callOrder)
}

func TestUpdater_Rollback_UpdatesStateToRolledBack(t *testing.T) {
	tmpDir := t.TempDir()

	binaryPath := filepath.Join(tmpDir, "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create backup
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "hostlink"), []byte("backup"), 0755))

	// Create current binary
	require.NoError(t, os.WriteFile(binaryPath, []byte("current"), 0755))

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           backupDir,
		StagingDir:          filepath.Join(tmpDir, "staging"),
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
	})
	u.serviceController = &mockServiceController{}

	err := u.Rollback(context.Background())
	require.NoError(t, err)

	// Verify state was updated
	stateWriter := update.NewStateWriter(update.StateConfig{StatePath: statePath})
	state, err := stateWriter.Read()
	require.NoError(t, err)
	assert.Equal(t, update.StateRolledBack, state.State)
}

func TestUpdater_UpdatePhases(t *testing.T) {
	tmpDir := t.TempDir()

	binaryPath := filepath.Join(tmpDir, "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	stagingDir := filepath.Join(tmpDir, "staging")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create current binary
	createTestBinary(t, binaryPath, []byte("old binary"))

	// Create staged tarball
	tarballPath := filepath.Join(stagingDir, "hostlink.tar.gz")
	createTestTarball(t, tarballPath, []byte("new binary v2.0.0"))

	// Track phase transitions
	var phases []string

	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: true, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	mockSvc := &mockServiceController{
		onStop:  func() { phases = append(phases, "stop") },
		onStart: func() { phases = append(phases, "start") },
	}

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           backupDir,
		StagingDir:          stagingDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(d time.Duration) {},
	})
	u.serviceController = mockSvc
	u.onPhaseChange = func(phase Phase) {
		phases = append(phases, string(phase))
	}

	err := u.Run(context.Background())
	require.NoError(t, err)

	// Verify phases executed in order
	expectedPhases := []string{
		string(PhaseAcquireLock),
		string(PhaseStopping),
		"stop",
		string(PhaseBackup),
		string(PhaseInstalling),
		string(PhaseStarting),
		"start",
		string(PhaseVerifying),
		string(PhaseCompleted),
	}
	assert.Equal(t, expectedPhases, phases)
}

func TestUpdater_Run_CancelledBeforeStop(t *testing.T) {
	tmpDir := t.TempDir()

	binaryPath := filepath.Join(tmpDir, "hostlink")
	createTestBinary(t, binaryPath, []byte("binary"))

	mockSvc := &mockServiceController{}

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           filepath.Join(tmpDir, "backup"),
		StagingDir:          filepath.Join(tmpDir, "staging"),
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           filepath.Join(tmpDir, "state.json"),
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(d time.Duration) {},
	})
	u.serviceController = mockSvc

	// Cancel context before calling Run
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := u.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, mockSvc.stopCalled, "service should not have been stopped")
	assert.False(t, mockSvc.startCalled, "service should not have been started")
}

func TestUpdater_Run_CancelledAfterStop(t *testing.T) {
	tmpDir := t.TempDir()

	binaryPath := filepath.Join(tmpDir, "hostlink")
	createTestBinary(t, binaryPath, []byte("binary"))
	stagingDir := filepath.Join(tmpDir, "staging")
	createTestTarball(t, filepath.Join(stagingDir, "hostlink.tar.gz"), []byte("new"))

	ctx, cancel := context.WithCancel(context.Background())

	mockSvc := &mockServiceController{
		onStop: func() {
			cancel() // Cancel after stop completes
		},
	}

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           filepath.Join(tmpDir, "backup"),
		StagingDir:          stagingDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           filepath.Join(tmpDir, "state.json"),
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(d time.Duration) {},
	})
	u.serviceController = mockSvc

	err := u.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, mockSvc.stopCalled)
	assert.True(t, mockSvc.startCalled, "service must be restarted after being stopped")
}

func TestUpdater_Run_CancelledAfterInstall(t *testing.T) {
	tmpDir := t.TempDir()

	binaryPath := filepath.Join(tmpDir, "hostlink")
	oldContent := []byte("old binary v1.0.0")
	createTestBinary(t, binaryPath, oldContent)
	stagingDir := filepath.Join(tmpDir, "staging")
	createTestTarball(t, filepath.Join(stagingDir, "hostlink.tar.gz"), []byte("new binary"))

	ctx, cancel := context.WithCancel(context.Background())

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           filepath.Join(tmpDir, "backup"),
		StagingDir:          stagingDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           filepath.Join(tmpDir, "state.json"),
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(d time.Duration) {},
	})

	// Cancel right after install phase begins
	u.serviceController = &mockServiceController{}
	u.onPhaseChange = func(phase Phase) {
		if phase == PhaseInstalling {
			// Let install complete, cancel right after
			// We use a goroutine to cancel after a tiny delay
		}
		if phase == PhaseStarting {
			// Cancel before start completes its inner work
			cancel()
		}
	}

	err := u.Run(ctx)

	// Context was cancelled after install, during start.
	// Start uses context.Background() so it should still succeed.
	// But since ctx is cancelled after start returns, verification is skipped.
	assert.ErrorIs(t, err, context.Canceled)

	// Service should have been started (start uses Background ctx)
	svc := u.serviceController.(*mockServiceController)
	assert.True(t, svc.startCalled)
}

func TestUpdater_Run_CancelledDuringVerification(t *testing.T) {
	tmpDir := t.TempDir()

	binaryPath := filepath.Join(tmpDir, "hostlink")
	createTestBinary(t, binaryPath, []byte("old binary"))
	stagingDir := filepath.Join(tmpDir, "staging")
	createTestTarball(t, filepath.Join(stagingDir, "hostlink.tar.gz"), []byte("new binary"))

	ctx, cancel := context.WithCancel(context.Background())

	// Health server that blocks until context is cancelled
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Wait for context cancellation to simulate slow health check
		<-ctx.Done()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer healthServer.Close()

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           filepath.Join(tmpDir, "backup"),
		StagingDir:          stagingDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           filepath.Join(tmpDir, "state.json"),
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(d time.Duration) {},
	})
	u.serviceController = &mockServiceController{}

	// Cancel during verification
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := u.Run(ctx)

	// Should return context.Canceled, NOT trigger rollback
	assert.ErrorIs(t, err, context.Canceled)

	// Binary should NOT be rolled back (service is running with new binary)
	content, err := os.ReadFile(binaryPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("new binary"), content)
}

func TestUpdater_Run_NoDoubleUnlock(t *testing.T) {
	tmpDir := t.TempDir()

	binaryPath := filepath.Join(tmpDir, "hostlink")
	createTestBinary(t, binaryPath, []byte("binary"))

	// Pre-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	u := NewUpdater(&UpdaterConfig{
		AgentBinaryPath:     binaryPath,
		BackupDir:           filepath.Join(tmpDir, "backup"),
		StagingDir:          filepath.Join(tmpDir, "staging"),
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           filepath.Join(tmpDir, "state.json"),
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(d time.Duration) {},
	})
	u.serviceController = &mockServiceController{}

	// This should not panic - only Run() unlocks via defer
	err := u.Run(ctx)
	assert.ErrorIs(t, err, context.Canceled)

	// Lock file should not exist (properly unlocked once)
	_, statErr := os.Stat(filepath.Join(tmpDir, "update.lock"))
	assert.True(t, os.IsNotExist(statErr), "lock should be released")
}

// Mock service controller for testing
type mockServiceController struct {
	stopCalled  bool
	startCalled bool
	stopErr     error
	startErr    error
	onStop      func()
	onStart     func()
}

func (m *mockServiceController) Stop(ctx context.Context) error {
	m.stopCalled = true
	if m.onStop != nil {
		m.onStop()
	}
	return m.stopErr
}

func (m *mockServiceController) Start(ctx context.Context) error {
	m.startCalled = true
	if m.onStart != nil {
		m.onStart()
	}
	return m.startErr
}

func newGzipWriter(w *os.File) *gzip.Writer {
	return gzip.NewWriter(w)
}

func newTarWriter(gw *gzip.Writer) *tar.Writer {
	return tar.NewWriter(gw)
}

type tarHeader = tar.Header
