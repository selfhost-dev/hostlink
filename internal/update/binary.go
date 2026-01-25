package update

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// BinaryPermissions is the file permission for installed binaries.
	BinaryPermissions = 0755
	// BackupFilename is the name of the backup file in the backup directory.
	BackupFilename = "hostlink"
	// AgentBinaryName is the binary name inside the agent tarball.
	AgentBinaryName = "hostlink"
	// MaxBinarySize is the maximum allowed size for an extracted binary (100MB).
	MaxBinarySize = 100 * 1024 * 1024
)

// BackupBinary copies the binary at srcPath to the backup directory.
// It creates the backup directory if it doesn't exist.
// It overwrites any existing backup using atomic rename to ensure
// the backup is never corrupted even if the process crashes mid-write.
func BackupBinary(srcPath, backupDir string) error {
	// Open source file
	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	// Get source file info for permissions
	srcInfo, err := src.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat source file: %w", err)
	}

	// Create backup directory
	if err := os.MkdirAll(backupDir, DirPermissions); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Generate temp file path for atomic write
	backupPath := filepath.Join(backupDir, BackupFilename)
	randSuffix, err := randomHex(8)
	if err != nil {
		return fmt.Errorf("failed to generate random suffix: %w", err)
	}
	tmpPath := backupPath + ".tmp." + randSuffix

	// Clean up temp file on error
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	// Create temp file
	dst, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return fmt.Errorf("failed to create temp backup file: %w", err)
	}

	// Copy content
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("failed to copy to backup: %w", err)
	}

	// Close before rename
	if err := dst.Close(); err != nil {
		return fmt.Errorf("failed to close temp backup file: %w", err)
	}

	// Atomic rename - replaces existing backup atomically
	if err := os.Rename(tmpPath, backupPath); err != nil {
		return fmt.Errorf("failed to finalize backup: %w", err)
	}

	// Success - don't clean up the temp file (it's been renamed)
	tmpPath = ""
	return nil
}

// InstallBinary extracts the binary from a tar.gz file and installs it atomically.
// It extracts the file named "hostlink" from the tarball to destPath.
// Uses atomic rename to ensure the install is atomic.
func InstallBinary(tarPath, destPath string) error {
	return installBinaryFromTarGz(tarPath, AgentBinaryName, destPath)
}

// installBinaryFromTarGz extracts a named binary from a tar.gz and installs it atomically.
func installBinaryFromTarGz(tarPath, binaryName, destPath string) error {
	// Create destination directory
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Generate temp file path
	randSuffix, err := randomHex(8)
	if err != nil {
		return fmt.Errorf("failed to generate random suffix: %w", err)
	}
	tmpPath := destPath + ".tmp." + randSuffix

	// Clean up temp file on error
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	// Extract binary to temp path
	if err := extractBinaryFromTarGz(tarPath, binaryName, tmpPath); err != nil {
		return fmt.Errorf("failed to extract binary: %w", err)
	}

	// Set permissions
	if err := os.Chmod(tmpPath, BinaryPermissions); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	// Success - don't clean up the temp file (it's been renamed)
	tmpPath = ""
	return nil
}

// InstallSelf copies the binary at srcPath to destPath atomically.
// srcPath is typically os.Executable() — the staged binary that is currently running.
// It writes to a temp file first, sets permissions to 0755, then does an atomic rename.
func InstallSelf(srcPath, destPath string) error {
	// Create destination directory
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Open source
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("failed to open source binary: %w", err)
	}
	defer src.Close()

	// Generate temp file path
	randSuffix, err := randomHex(8)
	if err != nil {
		return fmt.Errorf("failed to generate random suffix: %w", err)
	}
	tmpPath := destPath + ".tmp." + randSuffix

	// Clean up temp file on error
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	// Create temp file
	dst, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, BinaryPermissions)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	// Copy content
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("failed to copy binary: %w", err)
	}

	// Close before rename
	if err := dst.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Set permissions
	if err := os.Chmod(tmpPath, BinaryPermissions); err != nil {
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	// Success - don't clean up the temp file (it's been renamed)
	tmpPath = ""
	return nil
}

// RestoreBackup restores the binary from backup to destPath.
// Uses atomic rename for safe restoration.
func RestoreBackup(backupDir, destPath string) error {
	backupPath := filepath.Join(backupDir, BackupFilename)

	// Check backup exists
	srcInfo, err := os.Stat(backupPath)
	if err != nil {
		return fmt.Errorf("failed to stat backup: %w", err)
	}

	// Open backup file
	src, err := os.Open(backupPath)
	if err != nil {
		return fmt.Errorf("failed to open backup: %w", err)
	}
	defer src.Close()

	// Generate temp file path
	randSuffix, err := randomHex(8)
	if err != nil {
		return fmt.Errorf("failed to generate random suffix: %w", err)
	}
	tmpPath := destPath + ".tmp." + randSuffix

	// Clean up temp file on error
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	// Create temp file
	dst, err := os.OpenFile(tmpPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, srcInfo.Mode().Perm())
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}

	// Copy content
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return fmt.Errorf("failed to copy backup: %w", err)
	}

	// Close before rename
	if err := dst.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to restore backup: %w", err)
	}

	// Success
	tmpPath = ""
	return nil
}

// extractBinaryFromTarGz extracts the named binary from a tar.gz file.
func extractBinaryFromTarGz(tarPath, binaryName, destPath string) error {
	// Open tarball
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Create gzip reader
	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	// Create tar reader
	tr := tar.NewReader(gr)

	// Find and extract the named binary
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("%s binary not found in tarball", binaryName)
		}
		if err != nil {
			return fmt.Errorf("failed to read tar header: %w", err)
		}

		// Look for the named file (might be at root or in a subdirectory)
		baseName := filepath.Base(header.Name)
		if baseName == binaryName && header.Typeflag == tar.TypeReg {
			// Reject if declared size exceeds maximum
			if header.Size > MaxBinarySize {
				return fmt.Errorf("binary size %d exceeds maximum allowed size %d", header.Size, MaxBinarySize)
			}

			// Create destination file
			dst, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return fmt.Errorf("failed to create destination file: %w", err)
			}
			defer dst.Close()

			// Copy content with size limit as safety net (header.Size could lie)
			limited := io.LimitReader(tr, MaxBinarySize+1)
			n, err := io.Copy(dst, limited)
			if err != nil {
				return fmt.Errorf("failed to extract binary: %w", err)
			}
			if n > MaxBinarySize {
				return fmt.Errorf("binary size %d exceeds maximum allowed size %d", n, MaxBinarySize)
			}

			return nil
		}
	}
}

// randomHex generates a random hex string of the given length.
func randomHex(n int) (string, error) {
	bytes := make([]byte, n)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}
