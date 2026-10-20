// Package testutil provides helpers shared by tests across packages.
package testutil

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/labstack/gommon/log"

	"hostlink/internal/crypto"
)

// logCaptureMu serialises the tests that redirect the global logger.
var logCaptureMu sync.Mutex

// CaptureLogs redirects the global gommon logger into a buffer while fn runs
// and returns what was written. It is safe to call from tests that run in
// parallel with other log-capturing tests.
func CaptureLogs(t *testing.T, fn func()) string {
	t.Helper()

	logCaptureMu.Lock()
	defer logCaptureMu.Unlock()

	orig := log.Output()
	defer log.SetOutput(orig)

	var buf bytes.Buffer
	log.SetOutput(&buf)

	fn()

	return buf.String()
}

// SetupAgentIdentity writes a throwaway agent state and private key and points
// the app configuration at them, so that requests to the control plane can be
// signed. It returns the agent state directory and the private key path.
func SetupAgentIdentity(t *testing.T) (stateDir string, keyPath string) {
	t.Helper()

	tempDir := t.TempDir()
	keyPath = filepath.Join(tempDir, "agent.key")

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}
	if err := crypto.NewService().SavePrivateKey(privateKey, keyPath); err != nil {
		t.Fatalf("save private key: %v", err)
	}

	agentState := filepath.Join(tempDir, "agent.json")
	if err := os.WriteFile(agentState, []byte(`{"agent_id":"test-agent-123"}`), 0o600); err != nil {
		t.Fatalf("write agent state: %v", err)
	}

	t.Setenv("HOSTLINK_STATE_PATH", tempDir)
	t.Setenv("HOSTLINK_PRIVATE_KEY_PATH", keyPath)

	return tempDir, keyPath
}
