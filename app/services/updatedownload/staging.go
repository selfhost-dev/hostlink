package updatedownload

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// AgentTarballName is the filename for the staged agent tarball.
	AgentTarballName = "hostlink.tar.gz"
	// StagingDirPermissions is the permission mode for the staging directory.
	StagingDirPermissions = 0700
)

// StagingManager manages the staging area for update artifacts.
type StagingManager struct {
	basePath   string
	downloader *Downloader
}

// NewStagingManager creates a new StagingManager.
func NewStagingManager(basePath string, downloader *Downloader) *StagingManager {
	return &StagingManager{
		basePath:   basePath,
		downloader: downloader,
	}
}

// Prepare creates the staging directory with correct permissions (0700).
// This function is idempotent.
func (s *StagingManager) Prepare() error {
	if err := os.MkdirAll(s.basePath, StagingDirPermissions); err != nil {
		return fmt.Errorf("failed to create staging directory: %w", err)
	}

	// Ensure permissions are correct even if directory already exists
	if err := os.Chmod(s.basePath, StagingDirPermissions); err != nil {
		return fmt.Errorf("failed to set staging directory permissions: %w", err)
	}

	return nil
}

// StageAgent downloads and verifies the agent tarball to the staging area.
func (s *StagingManager) StageAgent(ctx context.Context, url, sha256 string) error {
	destPath := s.GetAgentPath()
	_, err := s.downloader.DownloadAndVerify(ctx, url, destPath, sha256)
	return err
}

// GetAgentPath returns the path to the staged agent tarball.
func (s *StagingManager) GetAgentPath() string {
	return filepath.Join(s.basePath, AgentTarballName)
}

// Cleanup removes the entire staging directory.
func (s *StagingManager) Cleanup() error {
	// Check if directory exists
	if _, err := os.Stat(s.basePath); os.IsNotExist(err) {
		return nil // Nothing to clean up
	}

	if err := os.RemoveAll(s.basePath); err != nil {
		return fmt.Errorf("failed to remove staging directory: %w", err)
	}

	return nil
}
