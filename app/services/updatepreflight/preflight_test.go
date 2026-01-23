package updatepreflight

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheck_BinaryWritable(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "hostlink")
	os.WriteFile(binaryPath, []byte("binary"), 0444)

	checker := New(PreflightConfig{
		AgentBinaryPath: binaryPath,
		UpdatesDir:      dir,
		StatFunc:        func(path string) (uint64, error) { return 1 << 30, nil },
	})

	result := checker.Check(10 * 1024 * 1024) // 10MB required
	if result.Passed {
		t.Error("expected Passed to be false when binary is not writable")
	}
	assertContainsError(t, result.Errors, "not writable")
}

func TestCheck_BinaryWritable_Passes(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "hostlink")
	os.WriteFile(binaryPath, []byte("binary"), 0755)

	checker := New(PreflightConfig{
		AgentBinaryPath: binaryPath,
		UpdatesDir:      dir,
		StatFunc:        func(path string) (uint64, error) { return 1 << 30, nil },
	})

	result := checker.Check(10 * 1024 * 1024)
	if !result.Passed {
		t.Errorf("expected Passed to be true, got errors: %v", result.Errors)
	}
}

func TestCheck_UpdatesDirWritable(t *testing.T) {
	dir := t.TempDir()
	readOnlyDir := filepath.Join(dir, "updates")
	os.MkdirAll(readOnlyDir, 0555)

	binaryPath := filepath.Join(dir, "hostlink")
	os.WriteFile(binaryPath, []byte("binary"), 0755)

	checker := New(PreflightConfig{
		AgentBinaryPath: binaryPath,
		UpdatesDir:      readOnlyDir,
		StatFunc:        func(path string) (uint64, error) { return 1 << 30, nil },
	})

	result := checker.Check(10 * 1024 * 1024)
	if result.Passed {
		t.Error("expected Passed to be false when updates dir is not writable")
	}
	assertContainsError(t, result.Errors, "not writable")
}

func TestCheck_DiskSpaceInsufficient(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "hostlink")
	os.WriteFile(binaryPath, []byte("binary"), 0755)

	checker := New(PreflightConfig{
		AgentBinaryPath: binaryPath,
		UpdatesDir:      dir,
		StatFunc:        func(path string) (uint64, error) { return 5 * 1024 * 1024, nil }, // 5MB available
	})

	// Require 10MB + 10MB buffer = 20MB, but only 5MB available
	result := checker.Check(10 * 1024 * 1024)
	if result.Passed {
		t.Error("expected Passed to be false when disk space is insufficient")
	}
	assertContainsError(t, result.Errors, "disk space")
}

func TestCheck_DiskSpaceSufficient(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "hostlink")
	os.WriteFile(binaryPath, []byte("binary"), 0755)

	checker := New(PreflightConfig{
		AgentBinaryPath: binaryPath,
		UpdatesDir:      dir,
		StatFunc:        func(path string) (uint64, error) { return 100 * 1024 * 1024, nil }, // 100MB
	})

	result := checker.Check(10 * 1024 * 1024)
	if !result.Passed {
		t.Errorf("expected Passed to be true, got errors: %v", result.Errors)
	}
}

func TestCheck_AllErrorsReported(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "hostlink")
	os.WriteFile(binaryPath, []byte("binary"), 0444) // not writable

	readOnlyDir := filepath.Join(dir, "updates")
	os.MkdirAll(readOnlyDir, 0555) // not writable

	checker := New(PreflightConfig{
		AgentBinaryPath: binaryPath,
		UpdatesDir:      readOnlyDir,
		StatFunc:        func(path string) (uint64, error) { return 1024, nil }, // tiny
	})

	result := checker.Check(10 * 1024 * 1024)
	if result.Passed {
		t.Error("expected Passed to be false")
	}
	// Should have 3 errors: binary not writable, dir not writable, disk space
	if len(result.Errors) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(result.Errors), result.Errors)
	}
}

func TestCheck_StatFuncError(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "hostlink")
	os.WriteFile(binaryPath, []byte("binary"), 0755)

	checker := New(PreflightConfig{
		AgentBinaryPath: binaryPath,
		UpdatesDir:      dir,
		StatFunc:        func(path string) (uint64, error) { return 0, errors.New("statfs failed") },
	})

	result := checker.Check(10 * 1024 * 1024)
	if result.Passed {
		t.Error("expected Passed to be false when stat fails")
	}
	assertContainsError(t, result.Errors, "disk space")
}

func TestCheck_BinaryNotExists(t *testing.T) {
	dir := t.TempDir()
	binaryPath := filepath.Join(dir, "nonexistent")

	checker := New(PreflightConfig{
		AgentBinaryPath: binaryPath,
		UpdatesDir:      dir,
		StatFunc:        func(path string) (uint64, error) { return 1 << 30, nil },
	})

	result := checker.Check(10 * 1024 * 1024)
	if result.Passed {
		t.Error("expected Passed to be false when binary does not exist")
	}
	assertContainsError(t, result.Errors, "not writable")
}

// assertContainsError checks that at least one error string contains the substring.
func assertContainsError(t *testing.T, errs []string, substr string) {
	t.Helper()
	for _, e := range errs {
		if contains(e, substr) {
			return
		}
	}
	t.Errorf("expected an error containing %q, got: %v", substr, errs)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
