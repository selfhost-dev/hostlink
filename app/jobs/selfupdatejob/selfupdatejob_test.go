package selfupdatejob

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hostlink/app/services/updatecheck"
	"hostlink/app/services/updatedownload"
	"hostlink/app/services/updatepreflight"
	"hostlink/internal/update"
)

// --- Registration Tests ---

func TestRegister_StartsGoroutineWithTrigger(t *testing.T) {
	triggered := make(chan struct{})
	job := NewWithConfig(SelfUpdateJobConfig{
		Trigger: func(ctx context.Context, fn func() error) {
			close(triggered)
			<-ctx.Done()
		},
	})

	ctx := context.Background()
	cancel := job.Register(ctx)
	defer cancel()

	select {
	case <-triggered:
		// success
	case <-time.After(time.Second):
		t.Fatal("trigger was not called within timeout")
	}
}

func TestRegister_ReturnsCancelFunc(t *testing.T) {
	var ctxCancelled atomic.Bool
	job := NewWithConfig(SelfUpdateJobConfig{
		Trigger: func(ctx context.Context, fn func() error) {
			<-ctx.Done()
			ctxCancelled.Store(true)
		},
	})

	ctx := context.Background()
	cancel := job.Register(ctx)

	cancel()
	time.Sleep(50 * time.Millisecond)

	if !ctxCancelled.Load() {
		t.Error("expected context to be cancelled after cancel() called")
	}
}

func TestShutdown_WaitsForGoroutine(t *testing.T) {
	var goroutineExited atomic.Bool
	job := NewWithConfig(SelfUpdateJobConfig{
		Trigger: func(ctx context.Context, fn func() error) {
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond) // simulate cleanup
			goroutineExited.Store(true)
		},
	})

	ctx := context.Background()
	job.Register(ctx)
	job.Shutdown()

	if !goroutineExited.Load() {
		t.Error("Shutdown() returned before goroutine exited")
	}
}

func TestRegister_RespectsParentContextCancellation(t *testing.T) {
	var ctxCancelled atomic.Bool
	job := NewWithConfig(SelfUpdateJobConfig{
		Trigger: func(ctx context.Context, fn func() error) {
			<-ctx.Done()
			ctxCancelled.Store(true)
		},
	})

	ctx, parentCancel := context.WithCancel(context.Background())
	job.Register(ctx)

	parentCancel()
	time.Sleep(50 * time.Millisecond)

	if !ctxCancelled.Load() {
		t.Error("expected trigger context to be cancelled when parent context is cancelled")
	}
}

// --- Trigger Tests ---

func TestDefaultTriggerConfig_FiveMinuteInterval(t *testing.T) {
	cfg := DefaultTriggerConfig()
	if cfg.Interval != 5*time.Minute {
		t.Errorf("expected default interval 5m, got %v", cfg.Interval)
	}
}

func TestTriggerWithConfig_CallsFnOnInterval(t *testing.T) {
	var callCount atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		TriggerWithConfig(ctx, func() error {
			callCount.Add(1)
			if callCount.Load() >= 3 {
				cancel()
			}
			return nil
		}, TriggerConfig{Interval: 10 * time.Millisecond})
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(time.Second):
		cancel()
		t.Fatal("trigger did not call fn 3 times within timeout")
	}

	if callCount.Load() < 3 {
		t.Errorf("expected at least 3 calls, got %d", callCount.Load())
	}
}

func TestTriggerWithConfig_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		TriggerWithConfig(ctx, func() error {
			return nil
		}, TriggerConfig{Interval: 10 * time.Millisecond})
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// success - trigger exited
	case <-time.After(time.Second):
		t.Fatal("trigger did not stop after context cancel")
	}
}

func TestTriggerWithConfig_ContinuesOnError(t *testing.T) {
	var callCount atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		TriggerWithConfig(ctx, func() error {
			callCount.Add(1)
			if callCount.Load() >= 3 {
				cancel()
			}
			return errors.New("some error")
		}, TriggerConfig{Interval: 10 * time.Millisecond})
		close(done)
	}()

	select {
	case <-done:
		// success
	case <-time.After(time.Second):
		cancel()
		t.Fatal("trigger did not continue after errors")
	}

	if callCount.Load() < 3 {
		t.Errorf("expected at least 3 calls despite errors, got %d", callCount.Load())
	}
}

// --- Update Flow Tests ---

func TestUpdateFlow_SkipsWhenNoUpdate(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{UpdateAvailable: false},
	}
	downloader := &mockDownloader{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:  checker,
		Downloader:     downloader,
		CurrentVersion: "1.0.0",
	})

	job.runUpdate(context.Background())

	if downloader.callCount.Load() > 0 {
		t.Error("downloader should not be called when no update available")
	}
}

func TestUpdateFlow_UnsupportedPlatform_ReturnsNilError(t *testing.T) {
	// When the control plane returns 400 (unsupported platform),
	// runUpdate should log WARN and return nil (not an error).
	checker := &mockUpdateChecker{
		err: updatecheck.ErrUnsupportedPlatform,
	}
	downloader := &mockDownloader{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:  checker,
		Downloader:     downloader,
		CurrentVersion: "1.0.0",
	})

	err := job.runUpdate(context.Background())

	if err != nil {
		t.Errorf("expected nil error for unsupported platform, got %v", err)
	}
	if downloader.callCount.Load() > 0 {
		t.Error("downloader should not be called when platform unsupported")
	}
}

func TestUpdateFlow_SkipsWhenPreflightFails(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{
			Passed: false,
			Errors: []string{"disk full"},
		},
	}
	downloader := &mockDownloader{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		CurrentVersion:   "1.0.0",
	})

	job.runUpdate(context.Background())

	if downloader.callCount.Load() > 0 {
		t.Error("downloader should not be called when preflight fails")
	}
}

func TestUpdateFlow_FullFlow(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc123",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{}
	installer := &mockBinaryInstaller{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    installer.install,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify full flow: check → preflight → lock → state(init) → download agent → extract binary → state(staged) → unlock → spawn
	if checker.callCount.Load() != 1 {
		t.Errorf("expected 1 check call, got %d", checker.callCount.Load())
	}
	if preflight.callCount.Load() != 1 {
		t.Errorf("expected 1 preflight call, got %d", preflight.callCount.Load())
	}
	if lock.lockCount.Load() != 1 {
		t.Errorf("expected 1 lock call, got %d", lock.lockCount.Load())
	}
	if downloader.callCount.Load() != 1 {
		t.Errorf("expected 1 download call (agent only), got %d", downloader.callCount.Load())
	}
	if installer.callCount.Load() != 1 {
		t.Errorf("expected 1 install call (extract from tarball), got %d", installer.callCount.Load())
	}
	if lock.unlockCount.Load() != 1 {
		t.Errorf("expected 1 unlock call, got %d", lock.unlockCount.Load())
	}
	if spawner.callCount.Load() != 1 {
		t.Errorf("expected 1 spawn call, got %d", spawner.callCount.Load())
	}

	// Verify state transitions
	states := state.getStates()
	if len(states) < 2 {
		t.Fatalf("expected at least 2 state writes, got %d", len(states))
	}
	if states[0] != update.StateInitialized {
		t.Errorf("expected first state to be Initialized, got %s", states[0])
	}
	if states[1] != update.StateStaged {
		t.Errorf("expected second state to be Staged, got %s", states[1])
	}
}

func TestUpdateFlow_UnlocksBeforeSpawn(t *testing.T) {
	var sequence []string
	var mu sync.Mutex

	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{
		onUnlock: func() {
			mu.Lock()
			sequence = append(sequence, "unlock")
			mu.Unlock()
		},
	}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{
		onSpawn: func() {
			mu.Lock()
			sequence = append(sequence, "spawn")
			mu.Unlock()
		},
	}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sequence) < 2 {
		t.Fatalf("expected at least 2 sequence entries, got %d: %v", len(sequence), sequence)
	}
	unlockIdx := -1
	spawnIdx := -1
	for i, s := range sequence {
		if s == "unlock" {
			unlockIdx = i
		}
		if s == "spawn" {
			spawnIdx = i
		}
	}
	if unlockIdx == -1 || spawnIdx == -1 {
		t.Fatalf("missing unlock or spawn in sequence: %v", sequence)
	}
	if unlockIdx > spawnIdx {
		t.Error("unlock must happen before spawn")
	}
}

func TestUpdateFlow_DownloadFailure(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{err: errors.New("download failed")}
	spawner := &mockSpawner{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	job.runUpdate(context.Background())

	if spawner.callCount.Load() > 0 {
		t.Error("spawner should not be called when download fails")
	}
	// Lock should still be released on failure
	if lock.unlockCount.Load() != 1 {
		t.Errorf("expected lock to be released on failure, unlock count: %d", lock.unlockCount.Load())
	}
}

func TestUpdateFlow_ExtractFailure_PreventsSpawn(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{}
	installer := &mockBinaryInstaller{err: errors.New("extraction failed")}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    installer.install,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	job.runUpdate(context.Background())

	if spawner.callCount.Load() > 0 {
		t.Error("spawner should not be called when extraction fails")
	}
	if lock.unlockCount.Load() != 1 {
		t.Errorf("expected lock to be released on failure, unlock count: %d", lock.unlockCount.Load())
	}
}

func TestUpdateFlow_SpawnArgs(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Spawn path should be the extracted binary in staging dir
	expectedSpawnPath := "/tmp/staging/hostlink"
	if spawner.lastPath != expectedSpawnPath {
		t.Errorf("expected spawn path %s, got %s", expectedSpawnPath, spawner.lastPath)
	}
	// Verify args are: upgrade --install-path <path> --update-id <uuid> --source-version <ver>
	if len(spawner.lastArgs) != 7 {
		t.Fatalf("expected 7 args, got %v", spawner.lastArgs)
	}
	if spawner.lastArgs[0] != "upgrade" {
		t.Errorf("arg[0]: expected 'upgrade', got %q", spawner.lastArgs[0])
	}
	if spawner.lastArgs[1] != "--install-path" || spawner.lastArgs[2] != "/usr/bin/hostlink" {
		t.Errorf("expected --install-path /usr/bin/hostlink, got %v", spawner.lastArgs[1:3])
	}
	if spawner.lastArgs[3] != "--update-id" {
		t.Errorf("arg[3]: expected '--update-id', got %q", spawner.lastArgs[3])
	}
	if spawner.lastArgs[4] == "" {
		t.Error("update-id should not be empty")
	}
	if spawner.lastArgs[5] != "--source-version" || spawner.lastArgs[6] != "1.0.0" {
		t.Errorf("expected --source-version 1.0.0, got %v", spawner.lastArgs[5:7])
	}

	// Verify UpdateID is consistent across state writes and spawn args
	writes := state.getWrites()
	if len(writes) < 2 {
		t.Fatalf("expected at least 2 state writes, got %d", len(writes))
	}
	updateID := spawner.lastArgs[4]
	if writes[0].UpdateID != updateID {
		t.Errorf("Initialized state UpdateID: expected %q, got %q", updateID, writes[0].UpdateID)
	}
	if writes[1].UpdateID != updateID {
		t.Errorf("Staged state UpdateID: expected %q, got %q", updateID, writes[1].UpdateID)
	}
}

func TestUpdateFlow_PassesDownloadSizeToPreflight(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
			AgentSize:       30 * 1024 * 1024, // 30MB
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := int64(30 * 1024 * 1024) // 30MB (agent only)
	if preflight.getLastRequiredSpace() != expected {
		t.Errorf("expected preflight requiredSpace %d, got %d", expected, preflight.getLastRequiredSpace())
	}
}

func TestUpdateFlow_FallsBackTo50MB_WhenSizeZero(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
			// AgentSize is zero (not provided by control plane)
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := int64(50 * 1024 * 1024) // 50MB fallback
	if preflight.getLastRequiredSpace() != expected {
		t.Errorf("expected preflight requiredSpace %d (50MB fallback), got %d", expected, preflight.getLastRequiredSpace())
	}
}

func TestUpdateFlow_AgentDestUsesCanonicalTarballName(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent tarball must use the canonical name
	expectedAgentDest := "/tmp/staging/" + updatedownload.AgentTarballName
	if len(downloader.destPaths) < 1 || downloader.destPaths[0] != expectedAgentDest {
		t.Errorf("expected agent dest path %q, got %v", expectedAgentDest, downloader.destPaths)
	}
}

func TestUpdateFlow_InstallBinaryArgs(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{}
	installer := &mockBinaryInstaller{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    installer.install,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify InstallBinary was called with tarball path and staging dest
	if installer.callCount.Load() != 1 {
		t.Fatalf("expected 1 install call, got %d", installer.callCount.Load())
	}
	expectedTarPath := "/tmp/staging/" + updatedownload.AgentTarballName
	if installer.lastTarPath != expectedTarPath {
		t.Errorf("expected tarPath %q, got %q", expectedTarPath, installer.lastTarPath)
	}
	// destPath should be staging/hostlink (the extracted binary to spawn)
	expectedDestPath := "/tmp/staging/hostlink"
	if installer.lastDestPath != expectedDestPath {
		t.Errorf("expected destPath %q, got %q", expectedDestPath, installer.lastDestPath)
	}
}

func TestUpdateFlow_ContextCancelledAfterDownload(t *testing.T) {
	var cancelCtx context.CancelFunc

	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{
		onCall: func(count int32) {
			if count == 1 {
				// Cancel context after download completes
				cancelCtx()
			}
		},
	}
	spawner := &mockSpawner{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancelCtx = cancel

	err := job.runUpdate(ctx)

	// Should return context.Canceled error
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled error, got: %v", err)
	}
	// Spawn should NOT have been called
	if spawner.callCount.Load() != 0 {
		t.Error("spawner should not be called when context is cancelled")
	}
}

func TestUpdateFlow_ContextCancelledAfterDownload_WritesErrorState(t *testing.T) {
	var cancelCtx context.CancelFunc

	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{
		onCall: func(count int32) {
			if count == 1 {
				cancelCtx()
			}
		},
	}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            func(string, []string) error { return nil },
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancelCtx = cancel

	job.runUpdate(ctx)

	// Verify error state was written
	writes := state.getWrites()
	if len(writes) < 2 {
		t.Fatalf("expected at least 2 state writes (init + error), got %d", len(writes))
	}
	lastWrite := writes[len(writes)-1]
	if lastWrite.State != update.StateInitialized {
		t.Errorf("expected last state to be Initialized, got %s", lastWrite.State)
	}
	if lastWrite.Error == nil {
		t.Error("expected Error field to be set on last state write")
	} else if *lastWrite.Error != context.Canceled.Error() {
		t.Errorf("expected error message %q, got %q", context.Canceled.Error(), *lastWrite.Error)
	}
}

func TestUpdateFlow_ExtractFailure_WritesErrorState(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	installer := &mockBinaryInstaller{err: errors.New("extraction failed: corrupt tarball")}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            func(string, []string) error { return nil },
		InstallBinary:    installer.install,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	job.runUpdate(context.Background())

	// Verify error state was written
	writes := state.getWrites()
	if len(writes) < 2 {
		t.Fatalf("expected at least 2 state writes (init + error), got %d", len(writes))
	}
	lastWrite := writes[len(writes)-1]
	if lastWrite.State != update.StateInitialized {
		t.Errorf("expected last state to be Initialized, got %s", lastWrite.State)
	}
	if lastWrite.Error == nil {
		t.Error("expected Error field to be set on last state write")
	} else if !strings.Contains(*lastWrite.Error, "extraction failed") {
		t.Errorf("expected error message to contain 'extraction failed', got %q", *lastWrite.Error)
	}
}

func TestUpdateFlow_DownloadFailure_WritesErrorState(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{err: errors.New("download failed: connection timeout")}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            func(string, []string) error { return nil },
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	job.runUpdate(context.Background())

	// Verify error state was written
	writes := state.getWrites()
	if len(writes) < 2 {
		t.Fatalf("expected at least 2 state writes (init + error), got %d", len(writes))
	}
	lastWrite := writes[len(writes)-1]
	if lastWrite.State != update.StateInitialized {
		t.Errorf("expected last state to be Initialized, got %s", lastWrite.State)
	}
	if lastWrite.Error == nil {
		t.Error("expected Error field to be set on last state write")
	} else if !strings.Contains(*lastWrite.Error, "download") {
		t.Errorf("expected error message to contain 'download', got %q", *lastWrite.Error)
	}
}

func TestUpdateFlow_ContextCancelledAfterExtract_WritesErrorState(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}

	var cancelCtx context.CancelFunc
	installer := &mockBinaryInstaller{
		onInstall: func() {
			// Cancel context after extraction succeeds
			cancelCtx()
		},
	}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            func(string, []string) error { return nil },
		InstallBinary:    installer.install,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancelCtx = cancel

	job.runUpdate(ctx)

	// Verify error state was written
	writes := state.getWrites()
	if len(writes) < 2 {
		t.Fatalf("expected at least 2 state writes (init + error), got %d", len(writes))
	}
	lastWrite := writes[len(writes)-1]
	if lastWrite.State != update.StateInitialized {
		t.Errorf("expected last state to be Initialized, got %s", lastWrite.State)
	}
	if lastWrite.Error == nil {
		t.Error("expected Error field to be set on last state write")
	} else if *lastWrite.Error != context.Canceled.Error() {
		t.Errorf("expected error message %q, got %q", context.Canceled.Error(), *lastWrite.Error)
	}
}

func TestUpdateFlow_ContextCancelledAfterStagedStateWrite_WritesErrorState(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	downloader := &mockDownloader{}

	var cancelCtx context.CancelFunc
	state := &mockStateWriter{
		onWrite: func(data update.StateData) {
			// Cancel context after staged state is written
			if data.State == update.StateStaged {
				cancelCtx()
			}
		},
	}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            func(string, []string) error { return nil },
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancelCtx = cancel

	job.runUpdate(ctx)

	// Verify error state was written after staged state
	writes := state.getWrites()
	if len(writes) < 3 {
		t.Fatalf("expected at least 3 state writes (init + staged + error), got %d", len(writes))
	}
	lastWrite := writes[len(writes)-1]
	if lastWrite.State != update.StateInitialized {
		t.Errorf("expected last state to be Initialized (error state), got %s", lastWrite.State)
	}
	if lastWrite.Error == nil {
		t.Error("expected Error field to be set on last state write")
	} else if *lastWrite.Error != context.Canceled.Error() {
		t.Errorf("expected error message %q, got %q", context.Canceled.Error(), *lastWrite.Error)
	}
}

func TestUpdateFlow_SpawnFailure_WritesErrorState(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{err: errors.New("spawn failed: exec format error")}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallBinary:    noopInstaller,
		CurrentVersion:   "1.0.0",
		InstallPath:      "/usr/bin/hostlink",
		StagingDir:       "/tmp/staging",
	})

	job.runUpdate(context.Background())

	// Verify error state was written after spawn failure
	writes := state.getWrites()
	if len(writes) < 3 {
		t.Fatalf("expected at least 3 state writes (init + staged + error), got %d", len(writes))
	}
	lastWrite := writes[len(writes)-1]
	if lastWrite.State != update.StateInitialized {
		t.Errorf("expected last state to be Initialized (error state), got %s", lastWrite.State)
	}
	if lastWrite.Error == nil {
		t.Error("expected Error field to be set on last state write")
	} else if !strings.Contains(*lastWrite.Error, "spawn") {
		t.Errorf("expected error message to contain 'spawn', got %q", *lastWrite.Error)
	}
}

// --- Helpers ---

// noopInstaller is a no-op InstallBinaryFunc for tests that don't care about extraction.
func noopInstaller(tarPath, destPath string) error { return nil }

// immediateTrigger calls fn exactly n times synchronously then returns.
func immediateTrigger(n int) TriggerFunc {
	return func(ctx context.Context, fn func() error) {
		for i := 0; i < n; i++ {
			fn()
		}
	}
}

// --- Mocks ---

type mockUpdateChecker struct {
	result    *updatecheck.UpdateInfo
	err       error
	callCount atomic.Int32
}

func (m *mockUpdateChecker) Check() (*updatecheck.UpdateInfo, error) {
	m.callCount.Add(1)
	return m.result, m.err
}

type mockPreflight struct {
	result            *updatepreflight.PreflightResult
	callCount         atomic.Int32
	lastRequiredSpace int64
	mu                sync.Mutex
}

func (m *mockPreflight) Check(requiredSpace int64) *updatepreflight.PreflightResult {
	m.callCount.Add(1)
	m.mu.Lock()
	m.lastRequiredSpace = requiredSpace
	m.mu.Unlock()
	return m.result
}

func (m *mockPreflight) getLastRequiredSpace() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastRequiredSpace
}

type mockLockManager struct {
	lockErr     error
	unlockErr   error
	lockCount   atomic.Int32
	unlockCount atomic.Int32
	onUnlock    func()
}

func (m *mockLockManager) TryLockWithRetry(expiration time.Duration, retries int, interval time.Duration) error {
	m.lockCount.Add(1)
	return m.lockErr
}

func (m *mockLockManager) Unlock() error {
	m.unlockCount.Add(1)
	if m.onUnlock != nil {
		m.onUnlock()
	}
	return m.unlockErr
}

type mockStateWriter struct {
	mu      sync.Mutex
	states  []update.State
	writes  []update.StateData
	err     error
	onWrite func(data update.StateData) // Called after each write
}

func (m *mockStateWriter) Write(data update.StateData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, data.State)
	m.writes = append(m.writes, data)
	if m.onWrite != nil {
		m.onWrite(data)
	}
	return m.err
}

func (m *mockStateWriter) getStates() []update.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]update.State, len(m.states))
	copy(result, m.states)
	return result
}

func (m *mockStateWriter) getWrites() []update.StateData {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]update.StateData, len(m.writes))
	copy(result, m.writes)
	return result
}

type mockDownloader struct {
	err        error
	failOnCall int32 // fail on this call number (1-indexed), 0 means all fail if err is set
	callCount  atomic.Int32
	destPaths  []string
	onCall     func(count int32) // called after each download with 1-indexed call number
	mu         sync.Mutex
}

func (m *mockDownloader) DownloadAndVerify(ctx context.Context, url, destPath, sha256 string) (*updatedownload.DownloadResult, error) {
	count := m.callCount.Add(1)
	m.mu.Lock()
	m.destPaths = append(m.destPaths, destPath)
	m.mu.Unlock()
	if m.onCall != nil {
		m.onCall(count)
	}
	if m.err != nil {
		if m.failOnCall == 0 || count == m.failOnCall {
			return nil, m.err
		}
	}
	return nil, nil
}

type mockSpawner struct {
	err       error
	callCount atomic.Int32
	lastPath  string
	lastArgs  []string
	onSpawn   func()
	mu        sync.Mutex
}

func (m *mockSpawner) spawn(updaterPath string, args []string) error {
	m.callCount.Add(1)
	m.mu.Lock()
	m.lastPath = updaterPath
	m.lastArgs = args
	m.mu.Unlock()
	if m.onSpawn != nil {
		m.onSpawn()
	}
	return m.err
}

type mockBinaryInstaller struct {
	err          error
	callCount    atomic.Int32
	lastTarPath  string
	lastDestPath string
	onInstall    func()
	mu           sync.Mutex
}

func (m *mockBinaryInstaller) install(tarPath, destPath string) error {
	m.callCount.Add(1)
	m.mu.Lock()
	m.lastTarPath = tarPath
	m.lastDestPath = destPath
	m.mu.Unlock()
	if m.onInstall != nil {
		m.onInstall()
	}
	return m.err
}
