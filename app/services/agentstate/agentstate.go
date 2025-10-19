package agentstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Global mutex for file operations
var fileMutex sync.RWMutex

type Operations interface {
	Save() error
	Load() error
	GetAgentID() string
	SetAgentID(id string) error
	Clear() error
}

type AgentState struct {
	AgentID      string            `json:"agent_id,omitempty"`
	LastSyncTime int64             `json:"last_sync_time,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	stateDir     string
}

func New(stateDir string) *AgentState {
	return &AgentState{
		stateDir: stateDir,
	}
}

func (s *AgentState) Save() error {
	fileMutex.Lock()
	defer fileMutex.Unlock()

	if err := os.MkdirAll(s.stateDir, 0700); err != nil {
		return err
	}

	data, err := json.Marshal(s)
	if err != nil {
		return err
	}

	stateFile := filepath.Join(s.stateDir, "agent.json")
	// Write to a temp file first, then rename for atomic operation
	tmpFile := stateFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return err
	}

	// Atomic rename
	return os.Rename(tmpFile, stateFile)
}

func (s *AgentState) Load() error {
	fileMutex.RLock()
	defer fileMutex.RUnlock()

	stateFile := filepath.Join(s.stateDir, "agent.json")

	data, err := os.ReadFile(stateFile)
	if err != nil {
		return err
	}

	return json.Unmarshal(data, s)
}

func (s *AgentState) GetAgentID() string {
	return s.AgentID
}

func (s *AgentState) SetAgentID(id string) error {
	s.AgentID = id
	return s.Save()
}

func (s *AgentState) Clear() error {
	fileMutex.Lock()
	defer fileMutex.Unlock()

	// Clear in-memory state
	s.AgentID = ""
	s.LastSyncTime = 0
	s.Metadata = nil

	// Remove the file
	stateFile := filepath.Join(s.stateDir, "agent.json")
	err := os.Remove(stateFile)

	// If file doesn't exist, that's fine
	if err != nil && !os.IsNotExist(err) {
		return err
	}

	return nil
}
