// Package upgrade implements the hostlink upgrade subcommand.
// It orchestrates the in-place upgrade of the hostlink binary:
// lock → backup → stop → install (self) → start → verify → cleanup.
package upgrade

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"hostlink/internal/update"
)

// Phase represents the current phase of the upgrade process.
type Phase string

const (
	PhaseAcquireLock Phase = "acquire_lock"
	PhaseBackup      Phase = "backup"
	PhaseStopping    Phase = "stopping"
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

// ServiceController interface for mocking in tests.
type ServiceController interface {
	Stop(ctx context.Context) error
	Start(ctx context.Context) error
	Exists(ctx context.Context) (bool, error)
}

// Config holds the configuration for the Upgrader.
type Config struct {
	InstallPath               string                                     // Target path (e.g. /usr/bin/hostlink)
	SelfPath                  string                                     // Path to the staged binary (os.Executable())
	BackupDir                 string                                     // Backup directory
	LockPath                  string                                     // Lock file path
	StatePath                 string                                     // State file path
	HealthURL                 string                                     // Health check URL
	TargetVersion             string                                     // Version to verify after upgrade
	UpdateID                  string                                     // Unique ID for this update operation
	SourceVersion             string                                     // Version being upgraded from
	ServiceStopTimeout        time.Duration                              // 30s
	ServiceStartTimeout       time.Duration                              // 30s
	HealthCheckRetries        int                                        // 5
	HealthCheckInterval       time.Duration                              // 5s
	HealthInitialWait         time.Duration                              // 5s
	LockRetries               int                                        // 5
	LockRetryInterval         time.Duration                              // 1s
	Logger                    *slog.Logger                               // Structured logger (nil = discard)
	RollbackStopRetries       int                                        // Retries for Stop during rollback (default: 3)
	RollbackStopRetryInterval time.Duration                              // Interval between stop retries (default: 1s)
	RollbackHealthCheckFunc   func(ctx context.Context) error            // Health check after rollback restart (nil = skip)
	SleepFunc                 func(context.Context, time.Duration) error // For testing; context-aware sleep (health checker)
	LockSleepFunc             func(time.Duration)                        // For testing; simple sleep (lock manager)
	InstallFunc               func(srcPath, destPath string) error       // Defaults to update.InstallSelf
}

// Upgrader orchestrates the upgrade process.
type Upgrader struct {
	config            *Config
	lock              *update.LockManager
	state             update.StateWriterInterface
	serviceController ServiceController
	healthChecker     *update.HealthChecker
	logger            *slog.Logger
	currentPhase      Phase
	startedAt         time.Time
	onPhaseChange     func(Phase) // For testing
}

// NewUpgrader creates a new Upgrader with the given configuration.
// Returns an error if required configuration is missing or invalid.
func NewUpgrader(cfg *Config) (*Upgrader, error) {
	if cfg.InstallPath == "" {
		return nil, fmt.Errorf("install-path cannot be empty")
	}

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
	if cfg.RollbackStopRetries == 0 {
		cfg.RollbackStopRetries = 3
	}
	if cfg.RollbackStopRetryInterval == 0 {
		cfg.RollbackStopRetryInterval = 1 * time.Second
	}
	if cfg.InstallFunc == nil {
		cfg.InstallFunc = update.InstallSelf
	}

	logger := cfg.Logger
	if logger == nil {
		logger = discardLogger()
	}

	return &Upgrader{
		config: cfg,
		logger: logger,
		lock: update.NewLockManager(update.LockConfig{
			LockPath:  cfg.LockPath,
			SleepFunc: cfg.LockSleepFunc,
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
	}, nil
}

// setPhase updates the current phase and calls the callback if set.
func (u *Upgrader) setPhase(phase Phase) {
	u.currentPhase = phase
	if u.onPhaseChange != nil {
		u.onPhaseChange(phase)
	}
}

// writeState writes state data to disk and logs a warning on error.
// State file is for observability only; write failures should not fail the upgrade.
func (u *Upgrader) writeState(data update.StateData) {
	if err := u.state.Write(data); err != nil {
		u.logger.Warn("failed to write state file", "error", err, "state", data.State)
	}
}

// Run executes the full upgrade process:
// lock → backup → stop → install (self) → start → verify → cleanup → unlock
//
// The key difference from the old updater: instead of extracting a binary from
// a tarball, it copies itself (the staged binary) to the install path.
func (u *Upgrader) Run(ctx context.Context) error {
	u.startedAt = time.Now()
	u.logger.Info("upgrade started",
		"target_version", u.config.TargetVersion,
		"install_path", u.config.InstallPath,
		"self_path", u.config.SelfPath,
	)

	// Clean up any leftover temp files first
	u.cleanupTempFiles()

	// Phase 1: Acquire lock
	u.setPhase(PhaseAcquireLock)
	if err := u.lock.TryLockWithRetry(DefaultLockExpiration, u.config.LockRetries, u.config.LockRetryInterval); err != nil {
		u.logger.Error("failed to acquire lock", "error", err)
		return fmt.Errorf("failed to acquire lock: %w", err)
	}
	defer u.lock.Unlock()
	u.logger.Info("lock acquired")

	// serviceStopped tracks whether we've stopped the service.
	serviceStopped := false

	// abort restarts the service (if stopped) using a background context.
	abort := func(reason error) error {
		if serviceStopped {
			u.serviceController.Start(context.Background())
		}
		return reason
	}

	// Check for cancellation before backup
	if ctx.Err() != nil {
		u.logger.Warn("cancelled before backup", "error", ctx.Err())
		return abort(ctx.Err())
	}

	// Phase 2: Backup current binary
	u.setPhase(PhaseBackup)
	if err := update.BackupBinary(u.config.InstallPath, u.config.BackupDir); err != nil {
		u.logger.Error("failed to backup binary", "error", err)
		return abort(fmt.Errorf("failed to backup binary: %w", err))
	}
	u.logger.Info("backup created", "backup_dir", u.config.BackupDir)

	// Check for cancellation before stop
	if ctx.Err() != nil {
		u.logger.Warn("cancelled before stop", "error", ctx.Err())
		return abort(ctx.Err())
	}

	// Phase 3: Stop service
	u.setPhase(PhaseStopping)
	if err := u.serviceController.Stop(ctx); err != nil {
		if ctx.Err() != nil {
			u.logger.Warn("cancelled during stop", "error", ctx.Err())
			return abort(ctx.Err())
		}
		u.logger.Error("failed to stop service", "error", err)
		return fmt.Errorf("failed to stop service: %w", err)
	}
	serviceStopped = true
	u.logger.Info("service stopped")

	// Check for cancellation after stop
	if ctx.Err() != nil {
		u.logger.Warn("cancelled after stop", "error", ctx.Err())
		return abort(ctx.Err())
	}

	// Phase 4: Install new binary (copy self to install path)
	u.setPhase(PhaseInstalling)
	if err := u.config.InstallFunc(u.config.SelfPath, u.config.InstallPath); err != nil {
		u.logger.Error("failed to install binary, rolling back", "error", err)
		installErr := fmt.Errorf("failed to install binary: %w", err)
		if rollbackErr := u.rollbackFrom(PhaseInstalling); rollbackErr != nil {
			return errors.Join(installErr, rollbackErr)
		}
		return installErr
	}
	u.logger.Info("binary installed", "install_path", u.config.InstallPath)

	// After install, the new binary is in place. Start it and exit.
	if ctx.Err() != nil {
		u.logger.Warn("cancelled after install, starting new service", "error", ctx.Err())
		u.serviceController.Start(context.Background())
		return ctx.Err()
	}

	// Phase 5: Start service
	// Use background context: even if cancelled, we must start the service.
	u.setPhase(PhaseStarting)
	if err := u.serviceController.Start(context.Background()); err != nil {
		u.logger.Error("failed to start service, rolling back", "error", err)
		startErr := fmt.Errorf("failed to start service: %w", err)
		if rollbackErr := u.rollbackFrom(PhaseStarting); rollbackErr != nil {
			return errors.Join(startErr, rollbackErr)
		}
		return startErr
	}
	serviceStopped = false
	u.logger.Info("service started")

	// Check for cancellation after start - service is running, skip verification.
	if ctx.Err() != nil {
		u.logger.Warn("cancelled after start, skipping verification", "error", ctx.Err())
		return ctx.Err()
	}

	// Phase 6: Verify health
	u.setPhase(PhaseVerifying)
	if err := u.healthChecker.WaitForHealth(ctx); err != nil {
		if ctx.Err() != nil {
			// Cancelled during verification - service is running, just exit.
			u.logger.Warn("cancelled during verification", "error", ctx.Err())
			return ctx.Err()
		}
		// Health check failed (not cancellation) - rollback
		u.logger.Error("health check failed, rolling back", "error", err)
		healthErr := fmt.Errorf("health check failed: %w", err)
		if rollbackErr := u.rollbackFrom(PhaseVerifying); rollbackErr != nil {
			return errors.Join(healthErr, rollbackErr)
		}
		return healthErr
	}
	u.logger.Info("health check passed")

	// Phase 7: Success
	u.setPhase(PhaseCompleted)
	u.writeState(update.StateData{
		State:         update.StateCompleted,
		UpdateID:      u.config.UpdateID,
		SourceVersion: u.config.SourceVersion,
		TargetVersion: u.config.TargetVersion,
		StartedAt:     u.startedAt,
		CompletedAt:   timePtr(time.Now()),
	})

	u.logger.Info("upgrade completed successfully", "target_version", u.config.TargetVersion)
	return nil
}

// rollbackFrom restores the backup and starts the service.
func (u *Upgrader) rollbackFrom(failedPhase Phase) error {
	u.setPhase(PhaseRollback)
	u.logger.Warn("rollback initiated", "failed_phase", string(failedPhase))

	u.writeState(update.StateData{
		State:         update.StateRollback,
		UpdateID:      u.config.UpdateID,
		SourceVersion: u.config.SourceVersion,
		TargetVersion: u.config.TargetVersion,
		StartedAt:     u.startedAt,
	})

	// Stop the service with retries
	var stopErr error
	for i := 0; i < u.config.RollbackStopRetries; i++ {
		if stopErr = u.serviceController.Stop(context.Background()); stopErr == nil {
			break
		}
		u.logger.Warn("rollback stop attempt failed", "attempt", i+1, "error", stopErr)
		if i < u.config.RollbackStopRetries-1 {
			if u.config.LockSleepFunc != nil {
				u.config.LockSleepFunc(u.config.RollbackStopRetryInterval)
			} else {
				time.Sleep(u.config.RollbackStopRetryInterval)
			}
		}
	}
	if stopErr != nil {
		u.logger.Warn("all rollback stop attempts failed, proceeding with restore", "error", stopErr)
	}

	// Restore backup
	if err := update.RestoreBackup(u.config.BackupDir, u.config.InstallPath); err != nil {
		u.logger.Error("failed to restore backup during rollback", "error", err)
		return fmt.Errorf("failed to restore backup: %w", err)
	}
	u.logger.Info("backup restored", "install_path", u.config.InstallPath)

	// Start service with old binary
	if err := u.serviceController.Start(context.Background()); err != nil {
		u.logger.Error("failed to start service after rollback", "error", err)
		return fmt.Errorf("failed to start service after rollback: %w", err)
	}
	u.logger.Info("service restarted after rollback")

	// Health check after rollback restart
	if u.config.RollbackHealthCheckFunc != nil {
		if err := u.config.RollbackHealthCheckFunc(context.Background()); err != nil {
			u.logger.Error("health check failed after rollback restart", "error", err)
			// Still write RolledBack state (binary was restored, service started)
			u.writeState(update.StateData{
				State:         update.StateRolledBack,
				UpdateID:      u.config.UpdateID,
				SourceVersion: u.config.SourceVersion,
				TargetVersion: u.config.TargetVersion,
				StartedAt:     u.startedAt,
				CompletedAt:   timePtr(time.Now()),
			})
			return fmt.Errorf("rollback health check failed: %w", err)
		}
		u.logger.Info("health check passed after rollback")
	}

	// Update state to rolled back
	u.writeState(update.StateData{
		State:         update.StateRolledBack,
		UpdateID:      u.config.UpdateID,
		SourceVersion: u.config.SourceVersion,
		TargetVersion: u.config.TargetVersion,
		StartedAt:     u.startedAt,
		CompletedAt:   timePtr(time.Now()),
	})

	u.logger.Info("rollback completed", "restored_version", u.config.SourceVersion)

	// Clean up temp files
	u.cleanupTempFiles()

	return nil
}

// cleanupTempFiles removes any leftover hostlink.tmp.* files.
func (u *Upgrader) cleanupTempFiles() {
	dir := filepath.Dir(u.config.InstallPath)
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
