package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateWriter_Write_ValidState(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	now := time.Now().Truncate(time.Second)
	data := StateData{
		UpdateID:      "test-update-123",
		State:         StateInitialized,
		SourceVersion: "v0.5.5",
		TargetVersion: "v0.6.0",
		StartedAt:     now,
	}

	err := sw.Write(data)
	require.NoError(t, err)

	// Verify file exists and has correct content
	content, err := os.ReadFile(statePath)
	require.NoError(t, err)

	var readData StateData
	err = json.Unmarshal(content, &readData)
	require.NoError(t, err)

	assert.Equal(t, data.UpdateID, readData.UpdateID)
	assert.Equal(t, data.State, readData.State)
	assert.Equal(t, data.SourceVersion, readData.SourceVersion)
	assert.Equal(t, data.TargetVersion, readData.TargetVersion)
}

func TestStateWriter_Write_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	data := StateData{
		UpdateID:      "test-123",
		State:         StateInitialized,
		SourceVersion: "v0.5.5",
		TargetVersion: "v0.6.0",
		StartedAt:     time.Now(),
	}

	err := sw.Write(data)
	require.NoError(t, err)

	// Check no temp files left behind
	entries, err := os.ReadDir(tmpDir)
	require.NoError(t, err)

	for _, entry := range entries {
		assert.Equal(t, "state.json", entry.Name(), "only state.json should exist")
	}
}

func TestStateWriter_Write_OverwritesExisting(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	// Write first state
	data1 := StateData{
		UpdateID:      "first-update",
		State:         StateInitialized,
		SourceVersion: "v0.5.5",
		TargetVersion: "v0.6.0",
		StartedAt:     time.Now(),
	}
	err := sw.Write(data1)
	require.NoError(t, err)

	// Write second state
	completedAt := time.Now()
	data2 := StateData{
		UpdateID:      "first-update",
		State:         StateCompleted,
		SourceVersion: "v0.5.5",
		TargetVersion: "v0.6.0",
		StartedAt:     data1.StartedAt,
		CompletedAt:   &completedAt,
	}
	err = sw.Write(data2)
	require.NoError(t, err)

	// Read and verify second state
	readData, err := sw.Read()
	require.NoError(t, err)
	assert.Equal(t, StateCompleted, readData.State)
	assert.NotNil(t, readData.CompletedAt)
}

func TestStateWriter_Write_CreatesParentDir(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "nested", "dir", "state.json")

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	data := StateData{
		UpdateID:      "test-123",
		State:         StateInitialized,
		SourceVersion: "v0.5.5",
		TargetVersion: "v0.6.0",
		StartedAt:     time.Now(),
	}

	err := sw.Write(data)
	require.NoError(t, err)

	// Verify file exists
	_, err = os.Stat(statePath)
	assert.NoError(t, err)
}

func TestStateWriter_Write_CorrectPermissions(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	data := StateData{
		UpdateID:      "test-123",
		State:         StateInitialized,
		SourceVersion: "v0.5.5",
		TargetVersion: "v0.6.0",
		StartedAt:     time.Now(),
	}

	err := sw.Write(data)
	require.NoError(t, err)

	info, err := os.Stat(statePath)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm(), "file should have 0600 permissions")
}

func TestStateWriter_Write_WithError(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	errMsg := "health check failed: version mismatch"
	data := StateData{
		UpdateID:      "test-123",
		State:         StateRolledBack,
		SourceVersion: "v0.5.5",
		TargetVersion: "v0.6.0",
		StartedAt:     time.Now(),
		Error:         &errMsg,
	}

	err := sw.Write(data)
	require.NoError(t, err)

	readData, err := sw.Read()
	require.NoError(t, err)
	require.NotNil(t, readData.Error)
	assert.Equal(t, errMsg, *readData.Error)
}

func TestStateWriter_Read_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	// Write state first
	now := time.Now().Truncate(time.Second)
	data := StateData{
		UpdateID:      "test-123",
		State:         StateCompleted,
		SourceVersion: "v0.5.5",
		TargetVersion: "v0.6.0",
		StartedAt:     now,
	}
	err := sw.Write(data)
	require.NoError(t, err)

	// Read it back
	readData, err := sw.Read()
	require.NoError(t, err)

	assert.Equal(t, data.UpdateID, readData.UpdateID)
	assert.Equal(t, data.State, readData.State)
	assert.Equal(t, data.SourceVersion, readData.SourceVersion)
	assert.Equal(t, data.TargetVersion, readData.TargetVersion)
}

func TestStateWriter_Read_NonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	// Read non-existent file should return zero value
	data, err := sw.Read()
	require.NoError(t, err)

	assert.Equal(t, StateData{}, data)
}

func TestStateWriter_Read_CorruptedFile(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	// Write corrupted content
	err := os.WriteFile(statePath, []byte("not valid json"), 0600)
	require.NoError(t, err)

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	// Should return error for corrupted file
	_, err = sw.Read()
	assert.Error(t, err)
}

func TestStateWriter_HumanReadableJSON(t *testing.T) {
	tmpDir := t.TempDir()
	statePath := filepath.Join(tmpDir, "state.json")

	sw := NewStateWriter(StateConfig{StatePath: statePath})

	data := StateData{
		UpdateID:      "test-123",
		State:         StateInitialized,
		SourceVersion: "v0.5.5",
		TargetVersion: "v0.6.0",
		StartedAt:     time.Now(),
	}

	err := sw.Write(data)
	require.NoError(t, err)

	// Read raw content and check it's indented (human-readable)
	content, err := os.ReadFile(statePath)
	require.NoError(t, err)

	// MarshalIndent produces newlines and spaces
	assert.Contains(t, string(content), "\n", "JSON should be formatted with newlines")
	assert.Contains(t, string(content), "  ", "JSON should be indented")
}
