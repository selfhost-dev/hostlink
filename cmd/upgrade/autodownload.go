package upgrade

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"hostlink/app/services/agentstate"
	"hostlink/app/services/requestsigner"
	"hostlink/app/services/updatecheck"
	"hostlink/app/services/updatedownload"
	"hostlink/config/appconf"
	"hostlink/internal/httpclient"
	"hostlink/internal/update"
)

// Sentinel errors for auto-download failures
var (
	ErrUpdateCheckFailed = errors.New("update check failed")
	ErrDownloadFailed    = errors.New("download failed")
	ErrExtractFailed     = errors.New("extract failed")
)

// UpdateCheckerInterface abstracts the update check client for testing.
type UpdateCheckerInterface interface {
	Check() (*updatecheck.UpdateInfo, error)
}

// DownloaderInterface abstracts the download functionality for testing.
type DownloaderInterface interface {
	DownloadAndVerify(ctx context.Context, url, destPath, sha256 string) error
}

// ExtractorInterface abstracts the tarball extraction for testing.
type ExtractorInterface interface {
	Extract(tarPath, destPath string) error
}

// AutoDownloader handles automatic download of the latest version.
type AutoDownloader struct {
	UpdateChecker UpdateCheckerInterface
	Downloader    DownloaderInterface
	Extractor     ExtractorInterface
	StagingDir    string
}

// DownloadLatestIfNeeded checks for updates and downloads the latest version if available.
// Returns the path to the staged binary, or empty string if no update is available.
func (ad *AutoDownloader) DownloadLatestIfNeeded(ctx context.Context) (string, error) {
	// Check for updates
	info, err := ad.UpdateChecker.Check()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrUpdateCheckFailed, err)
	}

	if !info.UpdateAvailable {
		return "", nil
	}

	// Create staging directory if it doesn't exist
	if err := os.MkdirAll(ad.StagingDir, update.DirPermissions); err != nil {
		return "", fmt.Errorf("failed to create staging directory: %w", err)
	}

	// Download tarball
	tarballPath := filepath.Join(ad.StagingDir, "hostlink.tar.gz")
	if err := ad.Downloader.DownloadAndVerify(ctx, info.AgentURL, tarballPath, info.AgentSHA256); err != nil {
		return "", fmt.Errorf("%w: %v", ErrDownloadFailed, err)
	}

	// Extract binary
	binaryPath := filepath.Join(ad.StagingDir, "hostlink")
	if err := ad.Extractor.Extract(tarballPath, binaryPath); err != nil {
		return "", fmt.Errorf("%w: %v", ErrExtractFailed, err)
	}

	return binaryPath, nil
}

// IsManualInvocation returns true if the upgrade command was invoked manually
// (e.g., from /usr/bin/hostlink) rather than spawned by selfupdatejob from staging.
func IsManualInvocation(selfPath, installPath, stagingDir string) bool {
	// Normalize all paths to handle trailing slashes, .., etc.
	selfPath = filepath.Clean(selfPath)
	installPath = filepath.Clean(installPath)
	stagingDir = filepath.Clean(stagingDir)

	// If running from the install path, it's manual
	if selfPath == installPath {
		return true
	}

	// If running from staging directory, it's spawned by selfupdatejob
	// Add separator to ensure directory boundary matching
	// e.g., /var/lib/staging should NOT match /var/lib/staging-test
	stagingDirWithSep := stagingDir + string(filepath.Separator)
	if strings.HasPrefix(selfPath, stagingDirWithSep) {
		return false
	}

	// Any other path is considered manual (e.g., /tmp/hostlink for testing)
	return true
}

// realDownloader wraps updatedownload.Downloader to implement DownloaderInterface.
type realDownloader struct {
	d *updatedownload.Downloader
}

func (r *realDownloader) DownloadAndVerify(ctx context.Context, url, destPath, sha256 string) error {
	_, err := r.d.DownloadAndVerify(ctx, url, destPath, sha256)
	return err
}

// realExtractor wraps update.InstallBinary to implement ExtractorInterface.
type realExtractor struct{}

func (r *realExtractor) Extract(tarPath, destPath string) error {
	return update.InstallBinary(tarPath, destPath)
}

// NewAutoDownloaderConfig holds configuration for creating an AutoDownloader.
type NewAutoDownloaderConfig struct {
	StagingDir string
	Logger     *slog.Logger
}

// NewAutoDownloader creates an AutoDownloader with real dependencies.
// It loads agent ID and control plane URL from the environment/config files.
func NewAutoDownloader(cfg NewAutoDownloaderConfig) (*AutoDownloader, error) {
	// Load agent ID from state file
	state := agentstate.New(appconf.AgentStatePath())
	if err := state.Load(); err != nil {
		return nil, fmt.Errorf("failed to load agent state: %w (is the agent registered?)", err)
	}
	agentID := state.GetAgentID()
	if agentID == "" {
		return nil, errors.New("agent not registered: run hostlink first to register")
	}

	// Get control plane URL
	controlPlaneURL := appconf.ControlPlaneURL()
	if controlPlaneURL == "" {
		return nil, errors.New("control plane URL not configured")
	}

	// Create request signer
	signer, err := requestsigner.New(appconf.AgentPrivateKeyPath(), agentID)
	if err != nil {
		return nil, fmt.Errorf("failed to create request signer: %w", err)
	}

	// Create HTTP client with agent headers
	client := httpclient.NewClient(30 * time.Second)

	// Create update checker
	checker, err := updatecheck.New(client, controlPlaneURL, agentID, signer)
	if err != nil {
		return nil, fmt.Errorf("failed to create update checker: %w", err)
	}

	// Create downloader
	downloader := updatedownload.NewDownloader(updatedownload.DefaultDownloadConfig())

	if cfg.Logger != nil {
		cfg.Logger.Info("auto-downloader initialized",
			"agent_id", agentID,
			"control_plane_url", controlPlaneURL,
		)
	}

	return &AutoDownloader{
		UpdateChecker: checker,
		Downloader:    &realDownloader{d: downloader},
		Extractor:     &realExtractor{},
		StagingDir:    cfg.StagingDir,
	}, nil
}

// ExecStagedBinary replaces the current process with the staged binary.
// This is used to hand off execution to the newly downloaded binary.
// The function never returns on success (process is replaced).
func ExecStagedBinary(stagedBinary string, args []string) error {
	// Prepend the binary path as argv[0]
	argv := append([]string{stagedBinary}, args...)

	// Replace current process with the staged binary
	// This never returns on success
	return syscall.Exec(stagedBinary, argv, os.Environ())
}
