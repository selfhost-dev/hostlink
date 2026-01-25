package upgrade

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"hostlink/app/services/updatecheck"
)

func TestIsManualInvocation(t *testing.T) {
	tests := []struct {
		name        string
		selfPath    string
		installPath string
		stagingDir  string
		want        bool
	}{
		{
			name:        "running from install path is manual",
			selfPath:    "/usr/bin/hostlink",
			installPath: "/usr/bin/hostlink",
			stagingDir:  "/var/lib/hostlink/updates/staging",
			want:        true,
		},
		{
			name:        "running from staging dir is spawned",
			selfPath:    "/var/lib/hostlink/updates/staging/hostlink",
			installPath: "/usr/bin/hostlink",
			stagingDir:  "/var/lib/hostlink/updates/staging",
			want:        false,
		},
		{
			name:        "running from different path is manual",
			selfPath:    "/tmp/hostlink",
			installPath: "/usr/bin/hostlink",
			stagingDir:  "/var/lib/hostlink/updates/staging",
			want:        true,
		},
		{
			name:        "running from custom staging dir is spawned",
			selfPath:    "/custom/staging/hostlink",
			installPath: "/usr/bin/hostlink",
			stagingDir:  "/custom/staging",
			want:        false,
		},
		{
			name:        "staging dir with similar prefix is manual",
			selfPath:    "/var/lib/hostlink/updates/staging-test/hostlink",
			installPath: "/usr/bin/hostlink",
			stagingDir:  "/var/lib/hostlink/updates/staging",
			want:        true,
		},
		{
			name:        "staging dir with trailing slash normalizes",
			selfPath:    "/var/lib/hostlink/updates/staging/hostlink",
			installPath: "/usr/bin/hostlink",
			stagingDir:  "/var/lib/hostlink/updates/staging/",
			want:        false,
		},
		{
			name:        "install path with dot-dot normalizes",
			selfPath:    "/usr/bin/../bin/hostlink",
			installPath: "/usr/bin/hostlink",
			stagingDir:  "/var/lib/hostlink/updates/staging",
			want:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsManualInvocation(tt.selfPath, tt.installPath, tt.stagingDir)
			if got != tt.want {
				t.Errorf("IsManualInvocation(%q, %q, %q) = %v, want %v",
					tt.selfPath, tt.installPath, tt.stagingDir, got, tt.want)
			}
		})
	}
}

// Mock implementations for testing

type mockUpdateChecker struct {
	info *updatecheck.UpdateInfo
	err  error
}

func (m *mockUpdateChecker) Check() (*updatecheck.UpdateInfo, error) {
	return m.info, m.err
}

type mockDownloader struct {
	err error
}

func (m *mockDownloader) DownloadAndVerify(ctx context.Context, url, destPath, sha256 string) error {
	if m.err != nil {
		return m.err
	}
	// Create a dummy file to simulate download
	return os.WriteFile(destPath, []byte("dummy binary"), 0755)
}

type mockExtractor struct {
	err error
}

func (m *mockExtractor) Extract(tarPath, destPath string) error {
	if m.err != nil {
		return m.err
	}
	// Create a dummy binary to simulate extraction
	return os.WriteFile(destPath, []byte("extracted binary"), 0755)
}

func TestAutoDownloader_NoUpdateAvailable(t *testing.T) {
	tmpDir := t.TempDir()
	stagingDir := filepath.Join(tmpDir, "staging")

	ad := &AutoDownloader{
		UpdateChecker: &mockUpdateChecker{
			info: &updatecheck.UpdateInfo{UpdateAvailable: false},
		},
		StagingDir: stagingDir,
	}

	stagedPath, err := ad.DownloadLatestIfNeeded(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stagedPath != "" {
		t.Errorf("expected empty staged path when no update, got %q", stagedPath)
	}
}

func TestAutoDownloader_UpdateAvailable_Downloads(t *testing.T) {
	tmpDir := t.TempDir()
	stagingDir := filepath.Join(tmpDir, "staging")

	ad := &AutoDownloader{
		UpdateChecker: &mockUpdateChecker{
			info: &updatecheck.UpdateInfo{
				UpdateAvailable: true,
				TargetVersion:   "1.0.0",
				AgentURL:        "https://example.com/hostlink.tar.gz",
				AgentSHA256:     "abc123",
			},
		},
		Downloader: &mockDownloader{},
		Extractor:  &mockExtractor{},
		StagingDir: stagingDir,
	}

	stagedPath, err := ad.DownloadLatestIfNeeded(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedPath := filepath.Join(stagingDir, "hostlink")
	if stagedPath != expectedPath {
		t.Errorf("staged path = %q, want %q", stagedPath, expectedPath)
	}

	// Verify the binary was created
	if _, err := os.Stat(stagedPath); os.IsNotExist(err) {
		t.Errorf("staged binary does not exist at %q", stagedPath)
	}
}

func TestAutoDownloader_CheckError(t *testing.T) {
	ad := &AutoDownloader{
		UpdateChecker: &mockUpdateChecker{
			err: errors.New("network error"),
		},
		StagingDir: t.TempDir(),
	}

	_, err := ad.DownloadLatestIfNeeded(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrUpdateCheckFailed) {
		t.Errorf("expected ErrUpdateCheckFailed, got %v", err)
	}
}

func TestAutoDownloader_DownloadError(t *testing.T) {
	tmpDir := t.TempDir()
	stagingDir := filepath.Join(tmpDir, "staging")

	ad := &AutoDownloader{
		UpdateChecker: &mockUpdateChecker{
			info: &updatecheck.UpdateInfo{
				UpdateAvailable: true,
				TargetVersion:   "1.0.0",
				AgentURL:        "https://example.com/hostlink.tar.gz",
				AgentSHA256:     "abc123",
			},
		},
		Downloader: &mockDownloader{err: errors.New("download failed")},
		StagingDir: stagingDir,
	}

	_, err := ad.DownloadLatestIfNeeded(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrDownloadFailed) {
		t.Errorf("expected ErrDownloadFailed, got %v", err)
	}
}

func TestAutoDownloader_ExtractError(t *testing.T) {
	tmpDir := t.TempDir()
	stagingDir := filepath.Join(tmpDir, "staging")

	ad := &AutoDownloader{
		UpdateChecker: &mockUpdateChecker{
			info: &updatecheck.UpdateInfo{
				UpdateAvailable: true,
				TargetVersion:   "1.0.0",
				AgentURL:        "https://example.com/hostlink.tar.gz",
				AgentSHA256:     "abc123",
			},
		},
		Downloader: &mockDownloader{},
		Extractor:  &mockExtractor{err: errors.New("extract failed")},
		StagingDir: stagingDir,
	}

	_, err := ad.DownloadLatestIfNeeded(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, ErrExtractFailed) {
		t.Errorf("expected ErrExtractFailed, got %v", err)
	}
}
