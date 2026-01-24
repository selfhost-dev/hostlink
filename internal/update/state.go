package update

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// State represents the current state of an update operation.
type State string

const (
	StateNotStarted  State = "NotStarted"
	StateInitialized State = "Initialized"
	StateStaged      State = "Staged"
	StateInstalled   State = "Installed"
	StateCompleted   State = "Completed"
	StateRollback    State = "Rollback"
	StateRolledBack  State = "RolledBack"
)

// StateData represents the update state persisted to disk.
type StateData struct {
	UpdateID      string     `json:"update_id"`
	State         State      `json:"state"`
	SourceVersion string     `json:"source_version"`
	TargetVersion string     `json:"target_version"`
	StartedAt     time.Time  `json:"started_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	Error         *string    `json:"error,omitempty"`
}

// StateWriterInterface defines the interface for state persistence.
// This interface allows mocking in tests.
type StateWriterInterface interface {
	Write(data StateData) error
	Read() (StateData, error)
}

// StateWriter manages the update state file for observability.
// Implements StateWriterInterface.
type StateWriter struct {
	statePath string
}

// StateConfig holds configuration for creating a StateWriter.
type StateConfig struct {
	StatePath string // e.g., /var/lib/hostlink/updates/state.json
}

// NewStateWriter creates a new StateWriter with the given configuration.
func NewStateWriter(cfg StateConfig) *StateWriter {
	return &StateWriter{
		statePath: cfg.StatePath,
	}
}

// Write persists the state data to disk atomically.
// Uses temp file + rename for atomic write.
func (s *StateWriter) Write(data StateData) error {
	// Ensure parent directory exists
	dir := filepath.Dir(s.statePath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Marshal to human-readable JSON
	content, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state data: %w", err)
	}

	// Write to temp file first
	randSuffix, err := randomString(8)
	if err != nil {
		return err
	}
	tmpFile := s.statePath + ".tmp." + randSuffix
	if err := os.WriteFile(tmpFile, content, 0600); err != nil {
		return fmt.Errorf("failed to write temp state file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tmpFile, s.statePath); err != nil {
		os.Remove(tmpFile) // Clean up on failure
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	return nil
}

// Read loads the state data from disk.
// Returns zero-value StateData and no error if file doesn't exist.
// Returns error if file exists but is corrupted.
func (s *StateWriter) Read() (StateData, error) {
	content, err := os.ReadFile(s.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return StateData{}, nil
		}
		return StateData{}, fmt.Errorf("failed to read state file: %w", err)
	}

	var data StateData
	if err := json.Unmarshal(content, &data); err != nil {
		return StateData{}, fmt.Errorf("failed to parse state file: %w", err)
	}

	return data, nil
}
