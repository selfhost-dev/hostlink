package selfupdatejob

import (
	"context"
	"errors"
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

func TestDefaultTriggerConfig_OneHourInterval(t *testing.T) {
	cfg := DefaultTriggerConfig()
	if cfg.Interval != 1*time.Hour {
		t.Errorf("expected default interval 1h, got %v", cfg.Interval)
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
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def456",
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
		InstallUpdater:   noopInstaller,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/tmp/updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify full flow: check → preflight → lock → state(init) → download agent → download updater → state(staged) → unlock → spawn
	if checker.callCount.Load() != 1 {
		t.Errorf("expected 1 check call, got %d", checker.callCount.Load())
	}
	if preflight.callCount.Load() != 1 {
		t.Errorf("expected 1 preflight call, got %d", preflight.callCount.Load())
	}
	if lock.lockCount.Load() != 1 {
		t.Errorf("expected 1 lock call, got %d", lock.lockCount.Load())
	}
	if downloader.callCount.Load() != 2 {
		t.Errorf("expected 2 download calls (agent + updater), got %d", downloader.callCount.Load())
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
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
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
		InstallUpdater:   noopInstaller,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/tmp/updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
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
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
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
		InstallUpdater:   noopInstaller,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/tmp/updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
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

func TestUpdateFlow_ChecksumMismatch(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	// First call (agent) succeeds, second (updater) fails
	downloader := &mockDownloader{failOnCall: 2, err: errors.New("checksum mismatch")}
	spawner := &mockSpawner{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallUpdater:   noopInstaller,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/tmp/updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
	})

	job.runUpdate(context.Background())

	if spawner.callCount.Load() > 0 {
		t.Error("spawner should not be called when checksum fails")
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
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
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
		InstallUpdater:   noopInstaller,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/opt/updater/hostlink-updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if spawner.lastPath != "/opt/updater/hostlink-updater" {
		t.Errorf("expected updater path /opt/updater/hostlink-updater, got %s", spawner.lastPath)
	}
	// Verify args contain -version and -base-dir
	foundVersion := false
	foundDir := false
	for i, arg := range spawner.lastArgs {
		if arg == "-version" && i+1 < len(spawner.lastArgs) && spawner.lastArgs[i+1] == "2.0.0" {
			foundVersion = true
		}
		if arg == "-base-dir" && i+1 < len(spawner.lastArgs) && spawner.lastArgs[i+1] == "/var/lib/hostlink/updates" {
			foundDir = true
		}
	}
	if !foundVersion {
		t.Errorf("expected -version 2.0.0 in spawn args, got %v", spawner.lastArgs)
	}
	if !foundDir {
		t.Errorf("expected -base-dir /var/lib/hostlink/updates in spawn args, got %v", spawner.lastArgs)
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
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
			UpdaterSize:     5 * 1024 * 1024, // 5MB
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
		InstallUpdater:   noopInstaller,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/tmp/updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := int64(35 * 1024 * 1024) // 30MB + 5MB
	if preflight.getLastRequiredSpace() != expected {
		t.Errorf("expected preflight requiredSpace %d, got %d", expected, preflight.getLastRequiredSpace())
	}
}

func TestUpdateFlow_FallsBackTo50MB_WhenSizesZero(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
			// AgentSize and UpdaterSize are zero (not provided by control plane)
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
		InstallUpdater:   noopInstaller,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/tmp/updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
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
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
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
		InstallUpdater:   noopInstaller,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/tmp/updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Agent tarball must use the canonical name expected by the updater
	expectedAgentDest := "/tmp/staging/" + updatedownload.AgentTarballName
	if len(downloader.destPaths) < 1 || downloader.destPaths[0] != expectedAgentDest {
		t.Errorf("expected agent dest path %q, got %v", expectedAgentDest, downloader.destPaths)
	}
}

func TestUpdateFlow_ExtractsUpdaterBeforeSpawn(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{}
	installer := &mockUpdaterInstaller{}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallUpdater:   installer.install,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/opt/updater/hostlink-updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
	})

	err := job.runUpdate(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify InstallUpdater was called with correct args
	if installer.callCount.Load() != 1 {
		t.Fatalf("expected 1 install call, got %d", installer.callCount.Load())
	}
	expectedTarPath := "/tmp/staging/" + updatedownload.UpdaterTarballName
	if installer.lastTarPath != expectedTarPath {
		t.Errorf("expected tarPath %q, got %q", expectedTarPath, installer.lastTarPath)
	}
	if installer.lastDestPath != "/opt/updater/hostlink-updater" {
		t.Errorf("expected destPath %q, got %q", "/opt/updater/hostlink-updater", installer.lastDestPath)
	}
	// Verify spawn was still called (extraction didn't block it)
	if spawner.callCount.Load() != 1 {
		t.Errorf("expected 1 spawn call, got %d", spawner.callCount.Load())
	}
}

func TestUpdateFlow_ExtractFailure_PreventsSpawn(t *testing.T) {
	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
		},
	}
	preflight := &mockPreflight{
		result: &updatepreflight.PreflightResult{Passed: true},
	}
	lock := &mockLockManager{}
	state := &mockStateWriter{}
	downloader := &mockDownloader{}
	spawner := &mockSpawner{}
	installer := &mockUpdaterInstaller{err: errors.New("extraction failed")}

	job := NewWithConfig(SelfUpdateJobConfig{
		UpdateChecker:    checker,
		Downloader:       downloader,
		PreflightChecker: preflight,
		LockManager:      lock,
		StateWriter:      state,
		Spawn:            spawner.spawn,
		InstallUpdater:   installer.install,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/opt/updater/hostlink-updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
	})

	job.runUpdate(context.Background())

	// Extraction failed, so spawn should NOT be called
	if spawner.callCount.Load() != 0 {
		t.Error("spawner should not be called when extraction fails")
	}
	// Lock should still be released
	if lock.unlockCount.Load() != 1 {
		t.Errorf("expected lock to be released on failure, unlock count: %d", lock.unlockCount.Load())
	}
}

func TestUpdateFlow_ContextCancelledBetweenDownloads(t *testing.T) {
	var cancelCtx context.CancelFunc

	checker := &mockUpdateChecker{
		result: &updatecheck.UpdateInfo{
			UpdateAvailable: true,
			TargetVersion:   "2.0.0",
			AgentURL:        "https://example.com/agent.tar.gz",
			AgentSHA256:     "abc",
			UpdaterURL:      "https://example.com/updater.tar.gz",
			UpdaterSHA256:   "def",
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
				// Cancel context after first download (agent) completes
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
		InstallUpdater:   noopInstaller,
		CurrentVersion:   "1.0.0",
		UpdaterPath:      "/tmp/updater",
		StagingDir:       "/tmp/staging",
		BaseDir:          "/var/lib/hostlink/updates",
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
	// Second download (updater) should NOT have been called
	if downloader.callCount.Load() != 1 {
		t.Errorf("expected only 1 download call (agent), got %d", downloader.callCount.Load())
	}
	// Spawn should NOT have been called
	if spawner.callCount.Load() != 0 {
		t.Error("spawner should not be called when context is cancelled")
	}
}

// --- Helpers ---

// noopInstaller is a no-op InstallUpdaterFunc for tests that don't care about extraction.
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

func (m *mockUpdateChecker) Check(currentVersion string) (*updatecheck.UpdateInfo, error) {
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
	mu     sync.Mutex
	states []update.State
	err    error
}

func (m *mockStateWriter) Write(data update.StateData) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append(m.states, data.State)
	return m.err
}

func (m *mockStateWriter) getStates() []update.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]update.State, len(m.states))
	copy(result, m.states)
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

type mockUpdaterInstaller struct {
	err          error
	callCount    atomic.Int32
	lastTarPath  string
	lastDestPath string
	mu           sync.Mutex
}

func (m *mockUpdaterInstaller) install(tarPath, destPath string) error {
	m.callCount.Add(1)
	m.mu.Lock()
	m.lastTarPath = tarPath
	m.lastDestPath = destPath
	m.mu.Unlock()
	return m.err
}
