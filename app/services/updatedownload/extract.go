package updatedownload

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	// ErrPathTraversal is returned when a tar entry contains path traversal.
	ErrPathTraversal = errors.New("path traversal detected in archive")
	// ErrFileNotFound is returned when the requested file is not in the archive.
	ErrFileNotFound = errors.New("file not found in archive")
)

const (
	// ExtractDirPermissions is the permission mode for extraction directories.
	// Uses 0755 as a sensible default for general-purpose extraction (e.g., binaries
	// to /usr/bin/ need to be world-executable). Callers needing different permissions
	// should create the destination directory beforehand with desired permissions.
	ExtractDirPermissions = 0755
)

// ExtractTarGz extracts all files from a .tar.gz archive to destDir.
// Creates destDir if it doesn't exist with 0755 permissions.
// Returns error on invalid archive or path traversal attempt.
func ExtractTarGz(tarPath, destDir string) error {
	// Create destDir with correct permissions before extraction
	if err := os.MkdirAll(destDir, ExtractDirPermissions); err != nil {
		return fmt.Errorf("failed to create destination directory: %w", err)
	}
	// Ensure permissions are correct even if directory already exists
	if err := os.Chmod(destDir, ExtractDirPermissions); err != nil {
		return fmt.Errorf("failed to set destination directory permissions: %w", err)
	}

	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		// Security: check for path traversal
		if err := validatePath(hdr.Name); err != nil {
			return err
		}

		targetPath := filepath.Join(destDir, hdr.Name)

		// Ensure the target is within destDir (defense in depth)
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(destDir)) {
			return fmt.Errorf("%w: %s", ErrPathTraversal, hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(hdr.Mode)); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", targetPath, err)
			}
		case tar.TypeReg:
			if err := extractFile(tr, targetPath, os.FileMode(hdr.Mode)); err != nil {
				return err
			}
		}
	}

	return nil
}

// validatePath checks if a path is safe (no traversal).
// Note: On Linux, backslashes are valid filename characters, not separators.
// The defense-in-depth check in ExtractTarGz (filepath.Join + HasPrefix) handles
// any edge cases. This function provides an early rejection for obvious attacks.
func validatePath(path string) error {
	// Reject absolute paths (checks both / on Unix and drive letters on Windows)
	if filepath.IsAbs(path) {
		return fmt.Errorf("%w: absolute path %s", ErrPathTraversal, path)
	}

	// Reject paths with .. components
	// Using filepath.Clean normalizes the path and resolves .. where possible
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") {
		return fmt.Errorf("%w: %s", ErrPathTraversal, path)
	}

	// Also check for .. anywhere in the cleaned path (e.g., "foo/../..")
	if strings.Contains(cleanPath, string(filepath.Separator)+"..") {
		return fmt.Errorf("%w: %s", ErrPathTraversal, path)
	}

	return nil
}

// extractFile extracts a single file from the tar reader to the target path.
func extractFile(tr *tar.Reader, targetPath string, mode os.FileMode) error {
	// Create parent directory if needed
	if err := os.MkdirAll(filepath.Dir(targetPath), ExtractDirPermissions); err != nil {
		return fmt.Errorf("failed to create parent directory: %w", err)
	}

	f, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", targetPath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, tr); err != nil {
		return fmt.Errorf("failed to write file %s: %w", targetPath, err)
	}

	return nil
}

// ExtractFile extracts a single file from a .tar.gz archive to destPath.
// Uses atomic write (temp file + rename).
// Returns error if the file is not found in the archive.
func ExtractFile(tarPath, fileName, destPath string) error {
	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar entry: %w", err)
		}

		if hdr.Name == fileName || filepath.Base(hdr.Name) == fileName {
			return extractFileAtomic(tr, destPath, os.FileMode(hdr.Mode))
		}
	}

	return fmt.Errorf("%w: %s", ErrFileNotFound, fileName)
}

// extractFileAtomic extracts content to a temp file then renames atomically.
func extractFileAtomic(r io.Reader, destPath string, mode os.FileMode) error {
	// Ensure parent directory exists
	if err := os.MkdirAll(filepath.Dir(destPath), ExtractDirPermissions); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// Write to temp file
	tmpFile, err := os.CreateTemp(filepath.Dir(destPath), ".extract-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Clean up temp file on error
	defer func() {
		if tmpPath != "" {
			os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmpFile, r); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to write file: %w", err)
	}

	if err := tmpFile.Chmod(mode); err != nil {
		tmpFile.Close()
		return fmt.Errorf("failed to set permissions: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	tmpPath = "" // Prevent cleanup since rename succeeded
	return nil
}
