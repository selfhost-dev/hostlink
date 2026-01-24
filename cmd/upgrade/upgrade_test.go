package upgrade

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
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

func TestUpgrader_Run_HappyPath(t *testing.T) {
	tmpDir := t.TempDir()

	// Setup paths
	installPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	lockPath := filepath.Join(tmpDir, "update.lock")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create current binary at install path
	createTestBinary(t, installPath, []byte("old binary v1.0.0"))

	// Create "self" binary (the staged new binary)
	createTestBinary(t, selfPath, []byte("new binary v2.0.0"))

	// Mock health server
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: true, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           backupDir,
		LockPath:            lockPath,
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)

	// Mock service controller (no real systemctl)
	u.serviceController = &mockServiceController{}

	err = u.Run(context.Background())
	require.NoError(t, err)

	// Verify new binary is installed (should be a copy of selfPath)
	content, err := os.ReadFile(installPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("new binary v2.0.0"), content)

	// Verify backup exists
	backupContent, err := os.ReadFile(filepath.Join(backupDir, "hostlink"))
	require.NoError(t, err)
	assert.Equal(t, []byte("old binary v1.0.0"), backupContent)
}

func TestUpgrader_Run_RollbackOnHealthCheckFailure(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	lockPath := filepath.Join(tmpDir, "update.lock")
	statePath := filepath.Join(tmpDir, "state.json")

	oldContent := []byte("old binary v1.0.0")
	createTestBinary(t, installPath, oldContent)
	createTestBinary(t, selfPath, []byte("new binary v2.0.0"))

	// Mock health server that always returns unhealthy
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: false, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           backupDir,
		LockPath:            lockPath,
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{}

	err = u.Run(context.Background())
	assert.Error(t, err)

	// Verify rollback occurred - old binary should be restored
	content, err := os.ReadFile(installPath)
	require.NoError(t, err)
	assert.Equal(t, oldContent, content)
}

func TestUpgrader_Run_LockAcquisitionFailure(t *testing.T) {
	tmpDir := t.TempDir()

	lockPath := filepath.Join(tmpDir, "update.lock")

	// Acquire lock first to simulate contention
	otherLock := update.NewLockManager(update.LockConfig{LockPath: lockPath})
	require.NoError(t, otherLock.TryLock(1*time.Hour))
	defer otherLock.Unlock()

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
		SleepFunc:         func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)

	err = u.Run(context.Background())
	assert.Error(t, err)
	assert.ErrorIs(t, err, update.ErrLockAcquireFailed)
}

func TestUpgrader_Run_CleansUpTempFiles(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	lockPath := filepath.Join(tmpDir, "update.lock")
	statePath := filepath.Join(tmpDir, "state.json")

	createTestBinary(t, installPath, []byte("old binary"))
	createTestBinary(t, selfPath, []byte("new binary v2.0.0"))

	// Create leftover temp files
	binDir := filepath.Dir(installPath)
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "hostlink.tmp.abc123"), []byte("temp"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(binDir, "hostlink.tmp.def456"), []byte("temp"), 0755))

	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: true, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           backupDir,
		LockPath:            lockPath,
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{}

	err = u.Run(context.Background())
	require.NoError(t, err)

	// Verify temp files were cleaned up
	entries, err := os.ReadDir(binDir)
	require.NoError(t, err)
	for _, entry := range entries {
		assert.NotContains(t, entry.Name(), ".tmp.", "temp files should be cleaned up")
	}
}

func TestUpgrader_PhaseOrder(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	createTestBinary(t, installPath, []byte("old binary"))
	createTestBinary(t, selfPath, []byte("new binary v2.0.0"))

	var phases []string

	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: true, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	mockSvc := &mockServiceController{
		onStop:  func() { phases = append(phases, "svc_stop") },
		onStart: func() { phases = append(phases, "svc_start") },
	}

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           backupDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = mockSvc
	u.onPhaseChange = func(phase Phase) {
		phases = append(phases, string(phase))
	}

	err = u.Run(context.Background())
	require.NoError(t, err)

	expectedPhases := []string{
		string(PhaseAcquireLock),
		string(PhaseBackup),
		string(PhaseStopping),
		"svc_stop",
		string(PhaseInstalling),
		string(PhaseStarting),
		"svc_start",
		string(PhaseVerifying),
		string(PhaseCompleted),
	}
	assert.Equal(t, expectedPhases, phases)
}

func TestUpgrader_Run_CancelledBeforeStop(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	createTestBinary(t, installPath, []byte("binary"))
	createTestBinary(t, selfPath, []byte("new"))

	mockSvc := &mockServiceController{}

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
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = mockSvc

	// Cancel context before calling Run
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = u.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
	assert.False(t, mockSvc.stopCalled, "service should not have been stopped")
	assert.False(t, mockSvc.startCalled, "service should not have been started")
}

func TestUpgrader_Run_CancelledAfterStop(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	createTestBinary(t, installPath, []byte("binary"))
	createTestBinary(t, selfPath, []byte("new"))

	ctx, cancel := context.WithCancel(context.Background())

	mockSvc := &mockServiceController{
		onStop: func() {
			cancel() // Cancel after stop completes
		},
	}

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
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = mockSvc

	err = u.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
	assert.True(t, mockSvc.stopCalled)
	assert.True(t, mockSvc.startCalled, "service must be restarted after being stopped")
}

func TestUpgrader_Run_CancelledAfterInstall(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	createTestBinary(t, installPath, []byte("old binary"))
	createTestBinary(t, selfPath, []byte("new binary"))

	ctx, cancel := context.WithCancel(context.Background())

	mockSvc := &mockServiceController{}

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
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
		InstallFunc: func(srcPath, destPath string) error {
			// Do the real install, then cancel
			err := update.InstallSelf(srcPath, destPath)
			cancel()
			return err
		},
	})
	require.NoError(t, err)
	u.serviceController = mockSvc

	err = u.Run(ctx)

	assert.ErrorIs(t, err, context.Canceled)
	// Per spec: after install, start the new service (not rollback)
	assert.True(t, mockSvc.startCalled, "new service must be started after install")
	// Install path should contain the new binary (not rolled back)
	content, readErr := os.ReadFile(installPath)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("new binary"), content, "should not roll back after install")
}

func TestUpgrader_Run_CancelledDuringVerification(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	createTestBinary(t, installPath, []byte("old binary"))
	createTestBinary(t, selfPath, []byte("new binary"))

	ctx, cancel := context.WithCancel(context.Background())

	// Health server that blocks until context is cancelled
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-ctx.Done()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer healthServer.Close()

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
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{}

	// Cancel during verification
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err = u.Run(ctx)

	// Should return context.Canceled, NOT trigger rollback
	assert.ErrorIs(t, err, context.Canceled)

	// Binary should NOT be rolled back (service is running with new binary)
	content, err := os.ReadFile(installPath)
	require.NoError(t, err)
	assert.Equal(t, []byte("new binary"), content)
}

func TestUpgrader_Rollback_RestoresAndStartsService(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create backup
	backupContent := []byte("backup binary v1.0.0")
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "hostlink"), backupContent, 0755))

	// Create "broken" current binary
	require.NoError(t, os.WriteFile(installPath, []byte("broken"), 0755))

	var callOrder []string
	mockSvc := &mockServiceController{
		onStop:  func() { callOrder = append(callOrder, "stop") },
		onStart: func() { callOrder = append(callOrder, "start") },
	}

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            filepath.Join(tmpDir, "staging", "hostlink"),
		BackupDir:           backupDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	u.serviceController = mockSvc

	err = u.rollbackFrom(PhaseVerifying)
	require.NoError(t, err)

	// Verify binary was restored
	content, err := os.ReadFile(installPath)
	require.NoError(t, err)
	assert.Equal(t, backupContent, content)

	// Verify service was stopped then started
	assert.Equal(t, []string{"stop", "start"}, callOrder)
}

func TestUpgrader_Rollback_WritesRolledBackState(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "hostlink"), []byte("backup"), 0755))
	require.NoError(t, os.WriteFile(installPath, []byte("current"), 0755))

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            filepath.Join(tmpDir, "staging", "hostlink"),
		BackupDir:           backupDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		UpdateID:            "rollback-update-id",
		SourceVersion:       "v1.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{}
	u.startedAt = time.Now()

	err = u.rollbackFrom(PhaseVerifying)
	require.NoError(t, err)

	// Verify state was updated with full context
	stateWriter := update.NewStateWriter(update.StateConfig{StatePath: statePath})
	state, err := stateWriter.Read()
	require.NoError(t, err)
	assert.Equal(t, update.StateRolledBack, state.State)
	assert.Equal(t, "rollback-update-id", state.UpdateID)
	assert.Equal(t, "v1.0.0", state.SourceVersion)
	assert.Equal(t, "v2.0.0", state.TargetVersion)
	assert.False(t, state.StartedAt.IsZero(), "StartedAt should be set")
	require.NotNil(t, state.CompletedAt, "CompletedAt should be set")
}

func TestUpgrader_Run_WritesCompletedState(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	statePath := filepath.Join(tmpDir, "state.json")

	createTestBinary(t, installPath, []byte("old binary"))
	createTestBinary(t, selfPath, []byte("new binary v2.0.0"))

	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: true, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           filepath.Join(tmpDir, "backup"),
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		UpdateID:            "test-update-id",
		SourceVersion:       "v1.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{}

	beforeRun := time.Now()
	err = u.Run(context.Background())
	require.NoError(t, err)

	// Verify state was written with full context
	stateWriter := update.NewStateWriter(update.StateConfig{StatePath: statePath})
	state, err := stateWriter.Read()
	require.NoError(t, err)
	assert.Equal(t, update.StateCompleted, state.State)
	assert.Equal(t, "v2.0.0", state.TargetVersion)
	assert.Equal(t, "test-update-id", state.UpdateID)
	assert.Equal(t, "v1.0.0", state.SourceVersion)
	assert.False(t, state.StartedAt.IsZero(), "StartedAt should be set")
	assert.True(t, !state.StartedAt.Before(beforeRun), "StartedAt should be >= test start")
	require.NotNil(t, state.CompletedAt, "CompletedAt should be set")
}

func TestUpgrader_Rollback_RetriesStop(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create backup and current binary
	backupContent := []byte("backup binary v1.0.0")
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "hostlink"), backupContent, 0755))
	require.NoError(t, os.WriteFile(installPath, []byte("broken"), 0755))

	// Stop fails first 2 times, succeeds on 3rd
	mockSvc := &mockServiceController{
		stopErrs: []error{
			fmt.Errorf("stop failed attempt 1"),
			fmt.Errorf("stop failed attempt 2"),
			nil, // succeeds on 3rd attempt
		},
	}

	u, err := NewUpgrader(&Config{
		InstallPath:               installPath,
		SelfPath:                  filepath.Join(tmpDir, "staging", "hostlink"),
		BackupDir:                 backupDir,
		LockPath:                  filepath.Join(tmpDir, "update.lock"),
		StatePath:                 statePath,
		HealthURL:                 "http://localhost:8080/health",
		TargetVersion:             "v2.0.0",
		ServiceStopTimeout:        100 * time.Millisecond,
		ServiceStartTimeout:       100 * time.Millisecond,
		RollbackStopRetries:       3,
		RollbackStopRetryInterval: 10 * time.Millisecond,
		SleepFunc:                 func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = mockSvc

	err = u.rollbackFrom(PhaseVerifying)
	require.NoError(t, err)

	// Verify Stop was called 3 times (retried)
	assert.Equal(t, 3, mockSvc.stopCallCount)

	// Verify binary was restored
	content, err := os.ReadFile(installPath)
	require.NoError(t, err)
	assert.Equal(t, backupContent, content)
}

func TestUpgrader_Rollback_ProceedsAfterStopExhausted(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create backup and current binary
	backupContent := []byte("backup binary v1.0.0")
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "hostlink"), backupContent, 0755))
	require.NoError(t, os.WriteFile(installPath, []byte("broken"), 0755))

	// Stop always fails
	mockSvc := &mockServiceController{
		stopErr: fmt.Errorf("service stop permanently failing"),
	}

	u, err := NewUpgrader(&Config{
		InstallPath:               installPath,
		SelfPath:                  filepath.Join(tmpDir, "staging", "hostlink"),
		BackupDir:                 backupDir,
		LockPath:                  filepath.Join(tmpDir, "update.lock"),
		StatePath:                 statePath,
		HealthURL:                 "http://localhost:8080/health",
		TargetVersion:             "v2.0.0",
		ServiceStopTimeout:        100 * time.Millisecond,
		ServiceStartTimeout:       100 * time.Millisecond,
		RollbackStopRetries:       3,
		RollbackStopRetryInterval: 10 * time.Millisecond,
		SleepFunc:                 func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = mockSvc

	err = u.rollbackFrom(PhaseVerifying)
	require.NoError(t, err)

	// Verify Stop was retried the configured number of times
	assert.Equal(t, 3, mockSvc.stopCallCount)

	// Verify binary was still restored (proceeded despite stop failure)
	content, err := os.ReadFile(installPath)
	require.NoError(t, err)
	assert.Equal(t, backupContent, content)

	// Verify service start was attempted
	assert.True(t, mockSvc.startCalled)
}

func TestUpgrader_Rollback_HealthCheckAfterRestart(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create backup and current binary
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "hostlink"), []byte("old binary"), 0755))
	require.NoError(t, os.WriteFile(installPath, []byte("broken"), 0755))

	healthCheckCalled := false
	u, err := NewUpgrader(&Config{
		InstallPath:               installPath,
		SelfPath:                  filepath.Join(tmpDir, "staging", "hostlink"),
		BackupDir:                 backupDir,
		LockPath:                  filepath.Join(tmpDir, "update.lock"),
		StatePath:                 statePath,
		HealthURL:                 "http://localhost:8080/health",
		TargetVersion:             "v2.0.0",
		SourceVersion:             "v1.0.0",
		ServiceStopTimeout:        100 * time.Millisecond,
		ServiceStartTimeout:       100 * time.Millisecond,
		RollbackStopRetries:       1,
		RollbackStopRetryInterval: 10 * time.Millisecond,
		RollbackHealthCheckFunc: func(ctx context.Context) error {
			healthCheckCalled = true
			return nil // healthy
		},
		SleepFunc: func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{}
	u.startedAt = time.Now()

	err = u.rollbackFrom(PhaseVerifying)
	require.NoError(t, err)

	// Verify health check was called after restart
	assert.True(t, healthCheckCalled, "health check should be called after rollback restart")
}

func TestUpgrader_Rollback_HealthCheckFailure(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create backup and current binary
	require.NoError(t, os.MkdirAll(backupDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(backupDir, "hostlink"), []byte("old binary"), 0755))
	require.NoError(t, os.WriteFile(installPath, []byte("broken"), 0755))

	u, err := NewUpgrader(&Config{
		InstallPath:               installPath,
		SelfPath:                  filepath.Join(tmpDir, "staging", "hostlink"),
		BackupDir:                 backupDir,
		LockPath:                  filepath.Join(tmpDir, "update.lock"),
		StatePath:                 statePath,
		HealthURL:                 "http://localhost:8080/health",
		TargetVersion:             "v2.0.0",
		SourceVersion:             "v1.0.0",
		ServiceStopTimeout:        100 * time.Millisecond,
		ServiceStartTimeout:       100 * time.Millisecond,
		RollbackStopRetries:       1,
		RollbackStopRetryInterval: 10 * time.Millisecond,
		RollbackHealthCheckFunc: func(ctx context.Context) error {
			return fmt.Errorf("old binary unhealthy")
		},
		SleepFunc: func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)
	u.serviceController = &mockServiceController{}
	u.startedAt = time.Now()

	err = u.rollbackFrom(PhaseVerifying)

	// Rollback should return an error when health check fails
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unhealthy")

	// But state should still be written as RolledBack (binary was restored, service started)
	stateWriter := update.NewStateWriter(update.StateConfig{StatePath: statePath})
	state, stateErr := stateWriter.Read()
	require.NoError(t, stateErr)
	assert.Equal(t, update.StateRolledBack, state.State)
}

func TestNewUpgrader_RejectsEmptyInstallPath(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &Config{
		InstallPath: "", // Empty - should be rejected
		SelfPath:    filepath.Join(tmpDir, "self"),
		BackupDir:   filepath.Join(tmpDir, "backup"),
		LockPath:    filepath.Join(tmpDir, "lock"),
		StatePath:   filepath.Join(tmpDir, "state.json"),
	}

	_, err := NewUpgrader(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "install-path")
}

// Mock service controller for testing
type mockServiceController struct {
	stopCalled    bool
	startCalled   bool
	stopErr       error
	startErr      error
	existsVal     bool
	existsErr     error
	onStop        func()
	onStart       func()
	stopCallCount int
	stopErrs      []error // If set, returns errors sequentially (overrides stopErr)
}

func (m *mockServiceController) Stop(ctx context.Context) error {
	m.stopCalled = true
	m.stopCallCount++
	if m.onStop != nil {
		m.onStop()
	}
	if m.stopErrs != nil {
		idx := m.stopCallCount - 1
		if idx < len(m.stopErrs) {
			return m.stopErrs[idx]
		}
		return m.stopErrs[len(m.stopErrs)-1]
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

func (m *mockServiceController) Exists(ctx context.Context) (bool, error) {
	return m.existsVal, m.existsErr
}

// Mock state writer for testing
type mockStateWriter struct {
	writeErr   error
	readErr    error
	lastState  update.StateData
	writeCount int
}

func (m *mockStateWriter) Write(data update.StateData) error {
	m.writeCount++
	m.lastState = data
	return m.writeErr
}

func (m *mockStateWriter) Read() (update.StateData, error) {
	return m.lastState, m.readErr
}

func TestUpgrader_Run_StateWriteFailure_LogsWarningAndContinues(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "usr", "bin", "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	lockPath := filepath.Join(tmpDir, "update.lock")
	statePath := filepath.Join(tmpDir, "state.json")

	createTestBinary(t, installPath, []byte("old binary v1.0.0"))
	createTestBinary(t, selfPath, []byte("new binary v2.0.0"))

	// Mock health server that returns healthy
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: true, Version: "v2.0.0"})
	}))
	defer healthServer.Close()

	// Capture logs
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, nil))

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           backupDir,
		LockPath:            lockPath,
		StatePath:           statePath,
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

	// Inject mock state writer that fails
	mockState := &mockStateWriter{writeErr: fmt.Errorf("disk full")}
	u.state = mockState
	u.serviceController = &mockServiceController{}

	// Run should succeed despite state write failure
	err = u.Run(context.Background())
	require.NoError(t, err)

	// Verify state write was attempted
	assert.GreaterOrEqual(t, mockState.writeCount, 1, "state.Write should have been called")

	// Verify warning was logged
	logOutput := logBuf.String()
	assert.Contains(t, logOutput, "disk full", "should log the state write error")
	assert.Contains(t, logOutput, "WARN", "should log at WARN level")
}

func TestUpgrader_Run_InstallFailure_RollbackFails_ReturnsBothErrors(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create current binary
	createTestBinary(t, installPath, []byte("old binary"))
	// Create staging binary
	createTestBinary(t, selfPath, []byte("new binary"))

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           backupDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	// Mock service controller - start fails (rollback will fail when trying to restart)
	mockSvc := &mockServiceController{
		startErr: fmt.Errorf("start failed: systemd error"),
	}
	u.serviceController = mockSvc

	// Make install fail by making InstallFunc return an error
	u.config.InstallFunc = func(src, dst string) error {
		return fmt.Errorf("install failed: permission denied")
	}

	err = u.Run(context.Background())
	require.Error(t, err)

	// Should contain both install error and rollback error
	errStr := err.Error()
	assert.Contains(t, errStr, "install", "error should mention install failure")
	assert.Contains(t, errStr, "rollback", "error should mention rollback failure")
}

func TestUpgrader_Run_StartFailure_RollbackFails_ReturnsBothErrors(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create current binary
	createTestBinary(t, installPath, []byte("old binary"))
	// Create staging binary
	createTestBinary(t, selfPath, []byte("new binary"))

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           backupDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           "http://localhost:8080/health",
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
	})
	require.NoError(t, err)

	// Mock service controller - start always fails (both in Run and in rollback)
	mockSvc := &mockServiceController{
		startErr: fmt.Errorf("start failed: systemd error"),
	}
	u.serviceController = mockSvc

	err = u.Run(context.Background())
	require.Error(t, err)

	// Should contain both start error and rollback error
	errStr := err.Error()
	assert.Contains(t, errStr, "failed to start service:", "error should mention original start failure")
	assert.Contains(t, errStr, "failed to start service after rollback", "error should mention rollback start failure")
}

func TestUpgrader_Run_HealthCheckFailure_RollbackFails_ReturnsBothErrors(t *testing.T) {
	tmpDir := t.TempDir()

	installPath := filepath.Join(tmpDir, "hostlink")
	selfPath := filepath.Join(tmpDir, "staging", "hostlink")
	backupDir := filepath.Join(tmpDir, "backup")
	statePath := filepath.Join(tmpDir, "state.json")

	// Create current binary
	createTestBinary(t, installPath, []byte("old binary"))
	// Create staging binary
	createTestBinary(t, selfPath, []byte("new binary"))

	// Health server that always returns unhealthy
	healthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(update.HealthResponse{Ok: false, Version: ""})
	}))
	defer healthServer.Close()

	u, err := NewUpgrader(&Config{
		InstallPath:         installPath,
		SelfPath:            selfPath,
		BackupDir:           backupDir,
		LockPath:            filepath.Join(tmpDir, "update.lock"),
		StatePath:           statePath,
		HealthURL:           healthServer.URL,
		TargetVersion:       "v2.0.0",
		ServiceStopTimeout:  100 * time.Millisecond,
		ServiceStartTimeout: 100 * time.Millisecond,
		HealthCheckRetries:  1,
		HealthCheckInterval: 10 * time.Millisecond,
		HealthInitialWait:   1 * time.Millisecond,
		SleepFunc:           func(_ context.Context, _ time.Duration) error { return nil },
	})
	require.NoError(t, err)

	// Mock service controller - start succeeds first time (in Run), fails second time (in rollback)
	mockSvc := &startFailsOnSecondCallController{}
	u.serviceController = mockSvc

	err = u.Run(context.Background())
	require.Error(t, err)

	// Should contain both health check error and rollback error
	errStr := err.Error()
	assert.Contains(t, errStr, "health check failed", "error should mention health check failure")
	assert.Contains(t, errStr, "start failed during rollback", "error should mention rollback failure")
}

// startFailsOnSecondCallController is a mock that succeeds on first Start, fails on second
type startFailsOnSecondCallController struct {
	startCallCount int
}

func (m *startFailsOnSecondCallController) Stop(ctx context.Context) error {
	return nil
}

func (m *startFailsOnSecondCallController) Start(ctx context.Context) error {
	m.startCallCount++
	if m.startCallCount > 1 {
		return fmt.Errorf("start failed during rollback: systemd error")
	}
	return nil
}

func (m *startFailsOnSecondCallController) Exists(ctx context.Context) (bool, error) {
	return true, nil
}
