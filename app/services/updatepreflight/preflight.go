package updatepreflight

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

const (
	// diskSpaceBuffer is an additional 10MB buffer required beyond the stated requiredSpace.
	diskSpaceBuffer = 10 * 1024 * 1024
)

// StatFunc returns available bytes for a given path.
type StatFunc func(path string) (uint64, error)

// PreflightResult holds the results of pre-flight checks.
type PreflightResult struct {
	Passed bool
	Errors []string
}

// PreflightChecker runs pre-flight checks before an update.
type PreflightChecker struct {
	agentBinaryPath string
	updatesDir      string
	statFunc        StatFunc
}

// PreflightConfig holds configuration for the PreflightChecker.
type PreflightConfig struct {
	AgentBinaryPath string
	UpdatesDir      string
	StatFunc        StatFunc
}

// New creates a new PreflightChecker.
func New(cfg PreflightConfig) *PreflightChecker {
	return &PreflightChecker{
		agentBinaryPath: cfg.AgentBinaryPath,
		updatesDir:      cfg.UpdatesDir,
		statFunc:        cfg.StatFunc,
	}
}

// Check runs all pre-flight checks. requiredSpace is in bytes.
func (p *PreflightChecker) Check(requiredSpace int64) *PreflightResult {
	var errs []string

	if err := p.checkInstallPath(); err != nil {
		errs = append(errs, err.Error())
	}

	if err := p.checkDirWritable(); err != nil {
		errs = append(errs, err.Error())
	}

	if err := p.checkDiskSpace(requiredSpace); err != nil {
		errs = append(errs, err.Error())
	}

	return &PreflightResult{
		Passed: len(errs) == 0,
		Errors: errs,
	}
}

func (p *PreflightChecker) checkInstallPath() error {
	// Check that the binary exists
	if _, err := os.Stat(p.agentBinaryPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("agent binary does not exist at %s", p.agentBinaryPath)
		}
		return fmt.Errorf("cannot stat agent binary %s: %w", p.agentBinaryPath, err)
	}

	// Check that the directory is writable (needed for atomic rename during upgrade)
	dir := filepath.Dir(p.agentBinaryPath)
	if err := unix.Access(dir, unix.W_OK); err != nil {
		return fmt.Errorf("cannot write to install directory %s: %w", dir, err)
	}

	return nil
}

func (p *PreflightChecker) checkDirWritable() error {
	tmpFile, err := os.CreateTemp(p.updatesDir, "preflight-*")
	if err != nil {
		return fmt.Errorf("updates directory %s is not writable: %w", p.updatesDir, err)
	}
	name := tmpFile.Name()
	tmpFile.Close()
	os.Remove(name)
	return nil
}

func (p *PreflightChecker) checkDiskSpace(requiredSpace int64) error {
	available, err := p.statFunc(p.updatesDir)
	if err != nil {
		return fmt.Errorf("failed to check disk space: %w", err)
	}

	needed := uint64(requiredSpace) + diskSpaceBuffer
	if available < needed {
		return fmt.Errorf("insufficient disk space: need %d bytes, have %d bytes", needed, available)
	}
	return nil
}
