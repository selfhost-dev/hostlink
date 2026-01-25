package selfupdatejob

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"
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
	Check() (*updatecheck.UpdateInfo, error)
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

// SpawnFunc is a function that spawns a binary with the given args.
type SpawnFunc func(binaryPath string, args []string) error

// InstallBinaryFunc extracts a binary from a tarball to a destination path.
type InstallBinaryFunc func(tarPath, destPath string) error

// SelfUpdateJobConfig holds the configuration for the SelfUpdateJob.
type SelfUpdateJobConfig struct {
	Trigger          TriggerFunc
	UpdateChecker    UpdateCheckerInterface
	Downloader       DownloaderInterface
	PreflightChecker PreflightCheckerInterface
	LockManager      LockManagerInterface
	StateWriter      StateWriterInterface
	Spawn            SpawnFunc
	InstallBinary    InstallBinaryFunc
	CurrentVersion   string
	InstallPath      string // Target install path (e.g., /usr/bin/hostlink)
	StagingDir       string // Where to download tarballs and extract binary
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
	log.Infof("checking for updates (current_version=%s, os=%s, arch=%s)", j.config.CurrentVersion, runtime.GOOS, runtime.GOARCH)
	info, err := j.config.UpdateChecker.Check()
	if err != nil {
		// Log unsupported platform at WARN level and return nil (not an error condition)
		if errors.Is(err, updatecheck.ErrUnsupportedPlatform) {
			log.Warnf("update check failed: unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
			return nil
		}
		return fmt.Errorf("update check failed: %w", err)
	}
	if !info.UpdateAvailable {
		log.Infof("no update available, current version %s is up to date", j.config.CurrentVersion)
		return nil
	}

	updateID := uuid.NewString()
	log.Infof("update available: %s -> %s (update_id=%s, url=%s, size=%d)", j.config.CurrentVersion, info.TargetVersion, updateID, info.AgentURL, info.AgentSize)

	// Step 2: Pre-flight checks
	log.Infof("running preflight checks (update_id=%s)", updateID)
	requiredSpace := info.AgentSize
	if requiredSpace == 0 {
		requiredSpace = defaultRequiredSpace
	}
	result := j.config.PreflightChecker.Check(requiredSpace)
	if !result.Passed {
		log.Warnf("preflight checks failed (update_id=%s): %v", updateID, result.Errors)
		return fmt.Errorf("preflight checks failed: %v", result.Errors)
	}
	log.Infof("preflight checks passed (update_id=%s)", updateID)

	// Step 3: Acquire lock
	log.Infof("acquiring update lock (update_id=%s)", updateID)
	if err := j.config.LockManager.TryLockWithRetry(5*time.Minute, 3, 5*time.Second); err != nil {
		log.Warnf("failed to acquire update lock (update_id=%s): %v", updateID, err)
		return fmt.Errorf("failed to acquire update lock: %w", err)
	}
	log.Infof("update lock acquired (update_id=%s)", updateID)

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
		UpdateID:      updateID,
		SourceVersion: j.config.CurrentVersion,
		TargetVersion: info.TargetVersion,
	}); err != nil {
		return fmt.Errorf("failed to write initialized state: %w", err)
	}

	// Helper to write error state (best-effort, errors ignored)
	writeErrorState := func(errMsg string) {
		j.config.StateWriter.Write(update.StateData{
			State:         update.StateInitialized,
			UpdateID:      updateID,
			SourceVersion: j.config.CurrentVersion,
			TargetVersion: info.TargetVersion,
			Error:         &errMsg,
		})
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Step 5: Download agent tarball
	log.Infof("downloading agent tarball (update_id=%s, url=%s)", updateID, info.AgentURL)
	agentDest := filepath.Join(j.config.StagingDir, updatedownload.AgentTarballName)
	if _, err := j.config.Downloader.DownloadAndVerify(ctx, info.AgentURL, agentDest, info.AgentSHA256); err != nil {
		log.Errorf("failed to download agent (update_id=%s): %v", updateID, err)
		writeErrorState(fmt.Sprintf("failed to download agent: %s", err))
		return fmt.Errorf("failed to download agent: %w", err)
	}
	log.Infof("agent tarball downloaded successfully (update_id=%s, dest=%s)", updateID, agentDest)

	if err := ctx.Err(); err != nil {
		writeErrorState(err.Error())
		return err
	}

	// Step 6: Extract hostlink binary from tarball to staging dir
	log.Infof("extracting binary from tarball (update_id=%s)", updateID)
	stagedBinary := filepath.Join(j.config.StagingDir, "hostlink")
	if err := j.config.InstallBinary(agentDest, stagedBinary); err != nil {
		log.Errorf("failed to extract binary (update_id=%s): %v", updateID, err)
		writeErrorState(fmt.Sprintf("failed to extract binary from tarball: %s", err))
		return fmt.Errorf("failed to extract binary from tarball: %w", err)
	}
	log.Infof("binary extracted successfully (update_id=%s, staged=%s)", updateID, stagedBinary)

	if err := ctx.Err(); err != nil {
		writeErrorState(err.Error())
		return err
	}

	// Step 7: Write staged state
	if err := j.config.StateWriter.Write(update.StateData{
		State:         update.StateStaged,
		UpdateID:      updateID,
		SourceVersion: j.config.CurrentVersion,
		TargetVersion: info.TargetVersion,
	}); err != nil {
		return fmt.Errorf("failed to write staged state: %w", err)
	}

	if err := ctx.Err(); err != nil {
		writeErrorState(err.Error())
		return err
	}

	// Step 8: Release lock before spawning upgrade
	log.Infof("releasing update lock before spawn (update_id=%s)", updateID)
	j.config.LockManager.Unlock()
	locked = false

	// Step 9: Spawn staged binary with upgrade subcommand
	args := []string{"upgrade", "--install-path", j.config.InstallPath, "--update-id", updateID, "--source-version", j.config.CurrentVersion}
	log.Infof("spawning upgrade process (update_id=%s, binary=%s, args=%v)", updateID, stagedBinary, args)
	if err := j.config.Spawn(stagedBinary, args); err != nil {
		log.Errorf("failed to spawn upgrade (update_id=%s): %v", updateID, err)
		writeErrorState(err.Error())
		return fmt.Errorf("failed to spawn upgrade: %w", err)
	}

	log.Infof("upgrade spawned successfully (update_id=%s, target_version=%s)", updateID, info.TargetVersion)
	return nil
}
