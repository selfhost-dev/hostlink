package selfupdatejob

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	"hostlink/app/services/updatecheck"
	"hostlink/app/services/updatedownload"
	"hostlink/app/services/updatepreflight"
	"hostlink/internal/update"
)

const (
	// defaultRequiredSpace is the fallback disk space requirement (50MB) when the
	// control plane does not provide download sizes.
	defaultRequiredSpace = 50 * 1024 * 1024
)

// TriggerFunc is the function type for the job's scheduling strategy.
type TriggerFunc func(context.Context, func() error)

// UpdateCheckerInterface abstracts the update check client.
type UpdateCheckerInterface interface {
	Check(currentVersion string) (*updatecheck.UpdateInfo, error)
}

// DownloaderInterface abstracts the download and verify functionality.
type DownloaderInterface interface {
	DownloadAndVerify(ctx context.Context, url, destPath, sha256 string) (*updatedownload.DownloadResult, error)
}

// PreflightCheckerInterface abstracts pre-flight checks.
type PreflightCheckerInterface interface {
	Check(requiredSpace int64) *updatepreflight.PreflightResult
}

// LockManagerInterface abstracts the lock manager.
type LockManagerInterface interface {
	TryLockWithRetry(expiration time.Duration, retries int, interval time.Duration) error
	Unlock() error
}

// StateWriterInterface abstracts the state writer.
type StateWriterInterface interface {
	Write(data update.StateData) error
}

// SpawnFunc is a function that spawns the updater binary.
type SpawnFunc func(updaterPath string, args []string) error

// InstallUpdaterFunc is a function that extracts and installs the updater binary from a tarball.
type InstallUpdaterFunc func(tarPath, destPath string) error

// SelfUpdateJobConfig holds the configuration for the SelfUpdateJob.
type SelfUpdateJobConfig struct {
	Trigger          TriggerFunc
	UpdateChecker    UpdateCheckerInterface
	Downloader       DownloaderInterface
	PreflightChecker PreflightCheckerInterface
	LockManager      LockManagerInterface
	StateWriter      StateWriterInterface
	Spawn            SpawnFunc
	InstallUpdater   InstallUpdaterFunc
	CurrentVersion   string
	UpdaterPath      string // Where to install the extracted updater binary
	StagingDir       string // Where to download tarballs
	BaseDir          string // Base update directory (for -base-dir flag to updater)
}

// SelfUpdateJob periodically checks for and applies updates.
type SelfUpdateJob struct {
	config SelfUpdateJobConfig
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a SelfUpdateJob with default configuration.
func New() *SelfUpdateJob {
	return &SelfUpdateJob{
		config: SelfUpdateJobConfig{
			Trigger: Trigger,
		},
	}
}

// NewWithConfig creates a SelfUpdateJob with the given configuration.
func NewWithConfig(cfg SelfUpdateJobConfig) *SelfUpdateJob {
	if cfg.Trigger == nil {
		cfg.Trigger = Trigger
	}
	return &SelfUpdateJob{
		config: cfg,
	}
}

// Register starts the job goroutine and returns a cancel function.
func (j *SelfUpdateJob) Register(ctx context.Context) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	j.cancel = cancel

	j.wg.Add(1)
	go func() {
		defer j.wg.Done()
		j.config.Trigger(ctx, func() error {
			return j.runUpdate(ctx)
		})
	}()

	return cancel
}

// Shutdown cancels the job and waits for the goroutine to exit.
func (j *SelfUpdateJob) Shutdown() {
	if j.cancel != nil {
		j.cancel()
	}
	j.wg.Wait()
}

// runUpdate performs a single update check and apply cycle.
func (j *SelfUpdateJob) runUpdate(ctx context.Context) error {
	// Step 1: Check for updates
	info, err := j.config.UpdateChecker.Check(j.config.CurrentVersion)
	if err != nil {
		return fmt.Errorf("update check failed: %w", err)
	}
	if !info.UpdateAvailable {
		return nil
	}

	log.Infof("update available: %s -> %s", j.config.CurrentVersion, info.TargetVersion)

	// Step 2: Pre-flight checks
	requiredSpace := info.AgentSize + info.UpdaterSize
	if requiredSpace == 0 {
		requiredSpace = defaultRequiredSpace
	}
	result := j.config.PreflightChecker.Check(requiredSpace)
	if !result.Passed {
		return fmt.Errorf("preflight checks failed: %v", result.Errors)
	}

	// Step 3: Acquire lock
	if err := j.config.LockManager.TryLockWithRetry(5*time.Minute, 3, 5*time.Second); err != nil {
		return fmt.Errorf("failed to acquire update lock: %w", err)
	}

	// From here on, we must release the lock on any failure
	locked := true
	defer func() {
		if locked {
			j.config.LockManager.Unlock()
		}
	}()

	// Step 4: Write initialized state
	if err := j.config.StateWriter.Write(update.StateData{
		State:         update.StateInitialized,
		SourceVersion: j.config.CurrentVersion,
		TargetVersion: info.TargetVersion,
	}); err != nil {
		return fmt.Errorf("failed to write initialized state: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Step 5: Download agent tarball
	agentDest := filepath.Join(j.config.StagingDir, updatedownload.AgentTarballName)
	if _, err := j.config.Downloader.DownloadAndVerify(ctx, info.AgentURL, agentDest, info.AgentSHA256); err != nil {
		return fmt.Errorf("failed to download agent: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Step 6: Download updater tarball
	updaterDest := filepath.Join(j.config.StagingDir, updatedownload.UpdaterTarballName)
	if _, err := j.config.Downloader.DownloadAndVerify(ctx, info.UpdaterURL, updaterDest, info.UpdaterSHA256); err != nil {
		return fmt.Errorf("failed to download updater: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Step 7: Write staged state
	if err := j.config.StateWriter.Write(update.StateData{
		State:         update.StateStaged,
		SourceVersion: j.config.CurrentVersion,
		TargetVersion: info.TargetVersion,
	}); err != nil {
		return fmt.Errorf("failed to write staged state: %w", err)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Step 8: Extract updater binary from tarball
	if err := j.config.InstallUpdater(updaterDest, j.config.UpdaterPath); err != nil {
		return fmt.Errorf("failed to install updater binary: %w", err)
	}

	// Step 9: Release lock before spawning updater
	j.config.LockManager.Unlock()
	locked = false

	// Step 10: Spawn updater in its own process group
	args := []string{"-version", info.TargetVersion, "-base-dir", j.config.BaseDir}
	if err := j.config.Spawn(j.config.UpdaterPath, args); err != nil {
		return fmt.Errorf("failed to spawn updater: %w", err)
	}

	log.Infof("updater spawned for version %s", info.TargetVersion)
	return nil
}
