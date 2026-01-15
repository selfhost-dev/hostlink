package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hostlink/app/services/updatedownload"
	"hostlink/internal/update"
)

// Phase represents the current phase of the update process.
type Phase string

const (
	PhaseAcquireLock Phase = "acquire_lock"
	PhaseStopping    Phase = "stopping"
	PhaseBackup      Phase = "backup"
	PhaseInstalling  Phase = "installing"
	PhaseStarting    Phase = "starting"
	PhaseVerifying   Phase = "verifying"
	PhaseCompleted   Phase = "completed"
	PhaseRollback    Phase = "rollback"
)

// Default configuration values
const (
	DefaultLockRetries       = 5
	DefaultLockRetryInterval = 1 * time.Second
	DefaultLockExpiration    = 5 * time.Minute
)

// UpdaterConfig holds the configuration for the Updater.
type UpdaterConfig struct {
	AgentBinaryPath     string              // /usr/bin/hostlink
	BackupDir           string              // /var/lib/hostlink/updates/backup/
	StagingDir          string              // /var/lib/hostlink/updates/staging/
	LockPath            string              // /var/lib/hostlink/updates/update.lock
	StatePath           string              // /var/lib/hostlink/updates/state.json
	HealthURL           string              // http://localhost:8080/health
	TargetVersion       string              // Version to verify after update
	ServiceStopTimeout  time.Duration       // 30s
	ServiceStartTimeout time.Duration       // 30s
	HealthCheckRetries  int                 // 5
	HealthCheckInterval time.Duration       // 5s
	HealthInitialWait   time.Duration       // 5s
	LockRetries         int                 // 5
	LockRetryInterval   time.Duration       // 1s
	SleepFunc           func(time.Duration) // For testing
}

// ServiceController interface for mocking in tests
type ServiceController interface {
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
}

// Updater orchestrates the update process.
type Updater struct {
	config            *UpdaterConfig
	lock              *update.LockManager
	state             *update.StateWriter
	serviceController ServiceController
	healthChecker     *update.HealthChecker
	currentPhase      Phase
	onPhaseChange     func(Phase) // For testing
}

// NewUpdater creates a new Updater with the given configuration.
func NewUpdater(cfg *UpdaterConfig) *Updater {
	// Apply defaults
	if cfg.LockRetries == 0 {
		cfg.LockRetries = DefaultLockRetries
	}
	if cfg.LockRetryInterval == 0 {
		cfg.LockRetryInterval = DefaultLockRetryInterval
	}
	if cfg.ServiceStopTimeout == 0 {
		cfg.ServiceStopTimeout = update.DefaultStopTimeout
	}
	if cfg.ServiceStartTimeout == 0 {
		cfg.ServiceStartTimeout = update.DefaultStartTimeout
	}
	if cfg.HealthCheckRetries == 0 {
		cfg.HealthCheckRetries = update.DefaultHealthRetries
	}
	if cfg.HealthCheckInterval == 0 {
		cfg.HealthCheckInterval = update.DefaultHealthInterval
	}
	if cfg.HealthInitialWait == 0 {
		cfg.HealthInitialWait = update.DefaultInitialWait
	}

	return &Updater{
		config: cfg,
		lock: update.NewLockManager(update.LockConfig{
			LockPath: cfg.LockPath,
		}),
		state: update.NewStateWriter(update.StateConfig{
			StatePath: cfg.StatePath,
		}),
		serviceController: update.NewServiceController(update.ServiceConfig{
			ServiceName:  "hostlink",
			StopTimeout:  cfg.ServiceStopTimeout,
			StartTimeout: cfg.ServiceStartTimeout,
		}),
		healthChecker: update.NewHealthChecker(update.HealthConfig{
			URL:           cfg.HealthURL,
			TargetVersion: cfg.TargetVersion,
			MaxRetries:    cfg.HealthCheckRetries,
			RetryInterval: cfg.HealthCheckInterval,
			InitialWait:   cfg.HealthInitialWait,
			SleepFunc:     cfg.SleepFunc,
		}),
	}
}

// setPhase updates the current phase and calls the callback if set.
func (u *Updater) setPhase(phase Phase) {
	u.currentPhase = phase
	if u.onPhaseChange != nil {
		u.onPhaseChange(phase)
	}
}

// Run executes the full update process:
// lock → stop → backup → install → start → verify → cleanup → unlock
//
// Run owns all cleanup. If ctx is cancelled (e.g. by a signal), Run aborts
// between phases and ensures the service is left running.
func (u *Updater) Run(ctx context.Context) error {
	// Clean up any leftover temp files first
	u.cleanupTempFiles()

	// Phase 1: Acquire lock
	u.setPhase(PhaseAcquireLock)
	if err := u.lock.TryLockWithRetry(DefaultLockExpiration, u.config.LockRetries, u.config.LockRetryInterval); err != nil {
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer u.lock.Unlock()

	// serviceStopped tracks whether we've stopped the service.
	// If true, any abort path must restart it.
	serviceStopped := false

	// abort restarts the service (if stopped) using a background context
	// since the original ctx may be cancelled.
	abort := func(reason error) error {
		if serviceStopped {
			u.serviceController.Start(context.Background())
		}
		return reason
	}

	// Check for cancellation before stopping
	if ctx.Err() != nil {
		return abort(ctx.Err())
	}

	// Phase 2: Stop service
	u.setPhase(PhaseStopping)
	if err := u.serviceController.Stop(ctx); err != nil {
		if ctx.Err() != nil {
			return abort(ctx.Err())
		}
		return fmt.Errorf("failed to stop service: %w", err)
	}
	serviceStopped = true

	// Check for cancellation after stop
	if ctx.Err() != nil {
		return abort(ctx.Err())
	}

	// Phase 3: Backup current binary
	u.setPhase(PhaseBackup)
	if err := update.BackupBinary(u.config.AgentBinaryPath, u.config.BackupDir); err != nil {
		return abort(fmt.Errorf("failed to backup binary: %w", err))
	}

	// Check for cancellation after backup
	if ctx.Err() != nil {
		return abort(ctx.Err())
	}

	// Phase 4: Install new binary
	u.setPhase(PhaseInstalling)
	tarballPath := filepath.Join(u.config.StagingDir, updatedownload.AgentTarballName)
	if err := update.InstallBinary(tarballPath, u.config.AgentBinaryPath); err != nil {
		u.rollbackFrom(PhaseInstalling)
		return fmt.Errorf("failed to install binary: %w", err)
	}

	// Check for cancellation after install - rollback needed
	if ctx.Err() != nil {
		u.rollbackFrom(PhaseInstalling)
		return ctx.Err()
	}

	// Phase 5: Start service
	// Use background context: even if cancelled, we must start the service
	// since the binary is already installed.
	u.setPhase(PhaseStarting)
	if err := u.serviceController.Start(context.Background()); err != nil {
		u.rollbackFrom(PhaseStarting)
		return fmt.Errorf("failed to start service: %w", err)
	}
	serviceStopped = false

	// Check for cancellation after start - service is running,
	// skip verification and exit cleanly.
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// Phase 6: Verify health
	u.setPhase(PhaseVerifying)
	if err := u.healthChecker.WaitForHealth(ctx); err != nil {
		if ctx.Err() != nil {
			// Cancelled during verification - service is running, just exit.
			return ctx.Err()
		}
		// Health check failed (not due to cancellation) - rollback
		u.rollbackFrom(PhaseVerifying)
		return fmt.Errorf("health check failed: %w", err)
	}

	// Phase 7: Success!
	u.setPhase(PhaseCompleted)

	// Update state to completed
	u.state.Write(update.StateData{
		State:         update.StateCompleted,
		TargetVersion: u.config.TargetVersion,
		CompletedAt:   timePtr(time.Now()),
	})

	return nil
}

// Rollback restores the backup and starts the service.
// Uses a background context for all operations since this may be called
// after the original context is cancelled.
func (u *Updater) Rollback(ctx context.Context) error {
	return u.rollbackFrom(PhaseVerifying)
}

// rollbackFrom restores the backup and starts the service.
// Uses context.Background() for all operations since this is cleanup
// that must complete regardless of cancellation state.
func (u *Updater) rollbackFrom(failedPhase Phase) error {
	u.setPhase(PhaseRollback)

	// Update state to rollback in progress
	u.state.Write(update.StateData{
		State:         update.StateRollback,
		TargetVersion: u.config.TargetVersion,
	})

	// Stop the service first (best-effort) - it may still be running the bad binary
	u.serviceController.Stop(context.Background())

	// Restore backup
	if err := update.RestoreBackup(u.config.BackupDir, u.config.AgentBinaryPath); err != nil {
		return fmt.Errorf("failed to restore backup: %w", err)
	}

	// Start service with old binary (use background context - must complete)
	if err := u.serviceController.Start(context.Background()); err != nil {
		return fmt.Errorf("failed to start service after rollback: %w", err)
	}

	// Update state to rolled back
	u.state.Write(update.StateData{
		State:         update.StateRolledBack,
		TargetVersion: u.config.TargetVersion,
		CompletedAt:   timePtr(time.Now()),
	})

	// Clean up temp files
	u.cleanupTempFiles()

	return nil
}

// cleanupTempFiles removes any leftover hostlink.tmp.* files.
func (u *Updater) cleanupTempFiles() {
	dir := filepath.Dir(u.config.AgentBinaryPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}

	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "hostlink.tmp.") {
			os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func timePtr(t time.Time) *time.Time {
	return &t
}
