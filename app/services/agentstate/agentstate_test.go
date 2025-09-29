package agentstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestAgentState_SaveAndLoad(t *testing.T) {
	t.Run("should save agent state to file with correct permissions", func(t *testing.T) {
		testDir := setupTestDir(t)
		stateFile := filepath.Join(testDir, "agent.json")

		state := New(testDir)
		state.AgentID = "test-agent-123"

		err := state.Save()
		if err != nil {
			t.Fatalf("Failed to save state: %v", err)
		}

		info, err := os.Stat(stateFile)
		if err != nil {
			t.Fatalf("State file not created: %v", err)
		}

		perm := info.Mode().Perm()
		if perm != 0600 {
			t.Errorf("Expected permissions 0600, got %o", perm)
		}

		content, err := os.ReadFile(stateFile)
		if err != nil {
			t.Fatalf("Failed to read state file: %v", err)
		}

		var savedState AgentState
		if err := json.Unmarshal(content, &savedState); err != nil {
			t.Fatalf("Failed to parse saved state: %v", err)
		}

		if savedState.AgentID != "test-agent-123" {
			t.Errorf("Expected AgentID 'test-agent-123', got '%s'", savedState.AgentID)
		}
	})

	t.Run("should load existing agent state from file", func(t *testing.T) {
		testDir := setupTestDir(t)
		_ = setupTestFile(t, testDir, `{"agent_id":"loaded-agent-456"}`)

		state := New(testDir)
		err := state.Load()
		if err != nil {
			t.Fatalf("Failed to load state: %v", err)
		}

		if state.AgentID != "loaded-agent-456" {
			t.Errorf("Expected AgentID 'loaded-agent-456', got '%s'", state.AgentID)
		}
	})

	t.Run("should return error when file doesn't exist", func(t *testing.T) {
		testDir := setupTestDir(t)

		state := New(testDir)
		err := state.Load()

		if err == nil {
			t.Fatal("Expected error when loading non-existent file, got nil")
		}

		if !os.IsNotExist(err) {
			t.Errorf("Expected file not exist error, got: %v", err)
		}
	})

	t.Run("should handle corrupted JSON gracefully", func(t *testing.T) {
		testDir := setupTestDir(t)
		_ = setupTestFile(t, testDir, `{invalid json content}`)

		state := New(testDir)
		err := state.Load()

		if err == nil {
			t.Fatal("Expected error when loading corrupted JSON, got nil")
		}

		var syntaxErr *json.SyntaxError
		if !errors.As(err, &syntaxErr) {
			t.Errorf("Expected JSON syntax error, got: %v", err)
		}
	})

	t.Run("should update existing state preserving other fields", func(t *testing.T) {
		testDir := setupTestDir(t)

		// Create initial state with multiple fields
		initialState := New(testDir)
		initialState.AgentID = "initial-id"
		initialState.LastSyncTime = 1234567890
		initialState.Metadata = map[string]string{
			"region":  "us-west-2",
			"version": "1.0.0",
		}

		if err := initialState.Save(); err != nil {
			t.Fatalf("Failed to save initial state: %v", err)
		}

		// Load and update only one field
		updatedState := New(testDir)
		if err := updatedState.Load(); err != nil {
			t.Fatalf("Failed to load state: %v", err)
		}

		// Update only the AgentID
		updatedState.AgentID = "updated-id"
		if err := updatedState.Save(); err != nil {
			t.Fatalf("Failed to save updated state: %v", err)
		}

		// Load again and verify all fields
		finalState := New(testDir)
		if err := finalState.Load(); err != nil {
			t.Fatalf("Failed to load final state: %v", err)
		}

		if finalState.AgentID != "updated-id" {
			t.Errorf("Expected AgentID 'updated-id', got '%s'", finalState.AgentID)
		}

		if finalState.LastSyncTime != 1234567890 {
			t.Errorf("Expected LastSyncTime 1234567890, got %d", finalState.LastSyncTime)
		}

		if len(finalState.Metadata) != 2 {
			t.Errorf("Expected 2 metadata entries, got %d", len(finalState.Metadata))
		}

		if finalState.Metadata["region"] != "us-west-2" {
			t.Errorf("Expected region 'us-west-2', got '%s'", finalState.Metadata["region"])
		}

		if finalState.Metadata["version"] != "1.0.0" {
			t.Errorf("Expected version '1.0.0', got '%s'", finalState.Metadata["version"])
		}
	})

	t.Run("should create directory if it doesn't exist", func(t *testing.T) {
		// Create a temporary parent directory
		parentDir, err := os.MkdirTemp("", "agentstate-parent-*")
		if err != nil {
			t.Fatalf("Failed to create parent directory: %v", err)
		}
		defer os.RemoveAll(parentDir)

		// Define a nested directory path that doesn't exist yet
		nestedDir := filepath.Join(parentDir, "subdir", "agentstate")

		// Verify the directory doesn't exist
		if _, err := os.Stat(nestedDir); !os.IsNotExist(err) {
			t.Fatal("Directory should not exist before test")
		}

		// Create state with non-existent directory
		state := New(nestedDir)
		state.AgentID = "test-agent"

		// Save should create the directory
		if err := state.Save(); err != nil {
			t.Fatalf("Failed to save state: %v", err)
		}

		// Verify directory was created
		info, err := os.Stat(nestedDir)
		if err != nil {
			t.Fatalf("Directory was not created: %v", err)
		}

		if !info.IsDir() {
			t.Error("Expected a directory to be created")
		}

		// Verify directory permissions
		perm := info.Mode().Perm()
		if perm != 0700 {
			t.Errorf("Expected directory permissions 0700, got %o", perm)
		}

		// Verify the state file exists
		stateFile := filepath.Join(nestedDir, "agent.json")
		if _, err := os.Stat(stateFile); err != nil {
			t.Errorf("State file was not created: %v", err)
		}
	})

	t.Run("should handle concurrent read/write operations safely", func(t *testing.T) {
		testDir := setupTestDir(t)

		// Create initial state
		initialState := New(testDir)
		initialState.AgentID = "initial"
		if err := initialState.Save(); err != nil {
			t.Fatalf("Failed to save initial state: %v", err)
		}

		// Number of concurrent operations
		numGoroutines := 10
		numOperations := 10

		errChan := make(chan error, numGoroutines*numOperations*2)
		done := make(chan bool)

		// Start concurrent readers and writers
		for i := range numGoroutines {
			// Writer goroutine
			go func(id int) {
				for j := range numOperations {
					state := New(testDir)
					state.AgentID = fmt.Sprintf("writer-%d-%d", id, j)
					state.LastSyncTime = int64(id*1000 + j)
					if err := state.Save(); err != nil {
						errChan <- fmt.Errorf("writer %d-%d failed: %v", id, j, err)
					}
				}
				done <- true
			}(i)

			// Reader goroutine
			go func(id int) {
				for j := range numOperations {
					state := New(testDir)
					if err := state.Load(); err != nil {
						errChan <- fmt.Errorf("reader %d-%d failed: %v", id, j, err)
					}
					// Verify we got valid data (not corrupted)
					if state.AgentID == "" {
						errChan <- fmt.Errorf("reader %d-%d got empty AgentID", id, j)
					}
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines to complete
		for range numGoroutines * 2 {
			<-done
		}
		close(errChan)

		// Check for errors
		for err := range errChan {
			t.Error(err)
		}

		// Verify final state is valid
		finalState := New(testDir)
		if err := finalState.Load(); err != nil {
			t.Fatalf("Failed to load final state: %v", err)
		}

		if finalState.AgentID == "" {
			t.Error("Final state has empty AgentID")
		}
	})
}

func TestAgentState_GetAgentID(t *testing.T) {
	t.Run("should return agent ID when present", func(t *testing.T) {
		testDir := setupTestDir(t)

		state := New(testDir)
		state.AgentID = "test-agent-123"

		agentID := state.GetAgentID()

		if agentID != "test-agent-123" {
			t.Errorf("Expected agent ID 'test-agent-123', got '%s'", agentID)
		}
	})

	t.Run("should return empty string when agent ID not set", func(t *testing.T) {
		testDir := setupTestDir(t)

		state := New(testDir)
		// Don't set AgentID, leave it empty

		agentID := state.GetAgentID()

		if agentID != "" {
			t.Errorf("Expected empty string, got '%s'", agentID)
		}
	})
}

func TestAgentState_SetAgentID(t *testing.T) {
	t.Run("should set agent ID and persist to file", func(t *testing.T) {
		testDir := setupTestDir(t)

		state := New(testDir)
		err := state.SetAgentID("new-agent-id")
		if err != nil {
			t.Fatalf("Failed to set agent ID: %v", err)
		}

		// Verify the ID was set in memory
		if state.AgentID != "new-agent-id" {
			t.Errorf("Expected agent ID 'new-agent-id' in memory, got '%s'", state.AgentID)
		}

		// Verify the file was created and persisted
		stateFile := filepath.Join(testDir, "agent.json")
		data, err := os.ReadFile(stateFile)
		if err != nil {
			t.Fatalf("Failed to read persisted state file: %v", err)
		}

		var persistedState AgentState
		if err := json.Unmarshal(data, &persistedState); err != nil {
			t.Fatalf("Failed to unmarshal persisted state: %v", err)
		}

		if persistedState.AgentID != "new-agent-id" {
			t.Errorf("Expected persisted agent ID 'new-agent-id', got '%s'", persistedState.AgentID)
		}

		// Load in a new instance to verify persistence
		newState := New(testDir)
		if err := newState.Load(); err != nil {
			t.Fatalf("Failed to load state: %v", err)
		}

		if newState.AgentID != "new-agent-id" {
			t.Errorf("Expected loaded agent ID 'new-agent-id', got '%s'", newState.AgentID)
		}
	})

	t.Run("should overwrite existing agent ID", func(t *testing.T) {
		testDir := setupTestDir(t)

		// Create initial state with an agent ID
		state := New(testDir)
		state.AgentID = "initial-id"
		state.LastSyncTime = 9876543210
		state.Metadata = map[string]string{"key": "value"}

		if err := state.Save(); err != nil {
			t.Fatalf("Failed to save initial state: %v", err)
		}

		// Set a new agent ID
		if err := state.SetAgentID("overwritten-id"); err != nil {
			t.Fatalf("Failed to overwrite agent ID: %v", err)
		}

		// Verify the ID was overwritten in memory
		if state.AgentID != "overwritten-id" {
			t.Errorf("Expected agent ID 'overwritten-id' in memory, got '%s'", state.AgentID)
		}

		// Load in a new instance to verify persistence
		newState := New(testDir)
		if err := newState.Load(); err != nil {
			t.Fatalf("Failed to load state: %v", err)
		}

		if newState.AgentID != "overwritten-id" {
			t.Errorf("Expected loaded agent ID 'overwritten-id', got '%s'", newState.AgentID)
		}

		// Verify other fields are preserved
		if newState.LastSyncTime != 9876543210 {
			t.Errorf("Expected LastSyncTime 9876543210, got %d", newState.LastSyncTime)
		}

		if newState.Metadata["key"] != "value" {
			t.Errorf("Expected metadata key 'value', got '%s'", newState.Metadata["key"])
		}
	})
}

func TestAgentState_Clear(t *testing.T) {
	t.Run("should remove agent state file", func(t *testing.T) {
		testDir := setupTestDir(t)

		// Create and save initial state
		state := New(testDir)
		state.AgentID = "test-agent-to-clear"
		state.LastSyncTime = 123456789

		if err := state.Save(); err != nil {
			t.Fatalf("Failed to save initial state: %v", err)
		}

		// Verify file exists
		stateFile := filepath.Join(testDir, "agent.json")
		if _, err := os.Stat(stateFile); err != nil {
			t.Fatalf("State file should exist before clear: %v", err)
		}

		// Clear the state
		if err := state.Clear(); err != nil {
			t.Fatalf("Failed to clear state: %v", err)
		}

		// Verify file is removed
		if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
			t.Error("State file should not exist after clear")
		}

		// Verify in-memory state is also cleared
		if state.AgentID != "" {
			t.Errorf("Expected AgentID to be cleared, got '%s'", state.AgentID)
		}

		if state.LastSyncTime != 0 {
			t.Errorf("Expected LastSyncTime to be 0, got %d", state.LastSyncTime)
		}
	})

	t.Run("should handle missing file gracefully", func(t *testing.T) {
		testDir := setupTestDir(t)

		// Create state without saving it (so no file exists)
		state := New(testDir)
		state.AgentID = "some-id"
		state.LastSyncTime = 999

		// Verify file doesn't exist
		stateFile := filepath.Join(testDir, "agent.json")
		if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
			t.Fatal("State file should not exist before test")
		}

		// Clear should not return an error even though file doesn't exist
		if err := state.Clear(); err != nil {
			t.Errorf("Clear should not fail when file doesn't exist: %v", err)
		}

		// Verify in-memory state is still cleared
		if state.AgentID != "" {
			t.Errorf("Expected AgentID to be cleared, got '%s'", state.AgentID)
		}

		if state.LastSyncTime != 0 {
			t.Errorf("Expected LastSyncTime to be 0, got %d", state.LastSyncTime)
		}
	})
}

// Helper functions for tests
func setupTestDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "agentstate-test-*")
	if err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}
	t.Cleanup(func() {
		os.RemoveAll(dir)
	})
	return dir
}

func setupTestFile(t *testing.T, dir string, content string) string {
	t.Helper()
	path := filepath.Join(dir, "agent.json")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}
	return path
}

