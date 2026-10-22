package agentregistrar

import (
	"testing"

	"hostlink/internal/httpclient"
)

// The registration shares the control-plane transport, so one dropped
// connection recovers every loop.
func TestRegistrar_UsesTheSharedControlPlaneTransport(t *testing.T) {
	registrar := New()

	if registrar.client.Transport != httpclient.SharedControlPlaneTransport() {
		t.Errorf("expected the shared control-plane transport, got %T", registrar.client.Transport)
	}
}
