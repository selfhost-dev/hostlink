package taskreporter

import (
	"testing"
	"time"

	"hostlink/internal/httpclient"
)

// The task result report shares the control-plane transport, so one dropped
// connection recovers every loop.
func TestTaskReporter_UsesTheSharedControlPlaneTransport(t *testing.T) {
	tempDir := t.TempDir()
	keys := setupTestKeys(t)
	agentState := setupTestAgentState(t, "test-agent-123")
	privateKeyPath := saveTestPrivateKey(t, tempDir, keys.privateKey)

	reporter, err := New(&Config{
		AgentState:      agentState,
		PrivateKeyPath:  privateKeyPath,
		ControlPlaneURL: "http://localhost:8080",
		Timeout:         10 * time.Second,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if reporter.client.Transport != httpclient.SharedControlPlaneTransport() {
		t.Errorf("expected the shared control-plane transport, got %T", reporter.client.Transport)
	}
}
