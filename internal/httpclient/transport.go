// Package httpclient provides HTTP client utilities with agent identification headers.
package httpclient

import (
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"hostlink/version"
)

// Capabilities the control plane may rely on, sent on every agent request (CapabilitiesHeader).
// sealed-env-v1: the agent opens a task's sealed env (selfhost #3168).
// mysql-tls-probe-v1: the agent probes MySQL over TLS (selfhost #3465).
// More are added at start by what is switched on (AddCapability): sql-stream-v1 when the SQL
// stream path's query service runs.
const (
	CapabilitiesHeader = "X-Agent-Capabilities"
	Capabilities       = "sealed-env-v1,mysql-tls-probe-v1"
)

var (
	capabilitiesMu    sync.RWMutex
	extraCapabilities []string
)

// AddCapability adds one capability to every request's list.
func AddCapability(c string) {
	capabilitiesMu.Lock()
	defer capabilitiesMu.Unlock()
	for _, have := range extraCapabilities {
		if have == c {
			return
		}
	}
	extraCapabilities = append(extraCapabilities, c)
}

// CapabilityList is the header's value: Capabilities and those added.
func CapabilityList() string {
	capabilitiesMu.RLock()
	defer capabilitiesMu.RUnlock()
	if len(extraCapabilities) == 0 {
		return Capabilities
	}
	return Capabilities + "," + strings.Join(extraCapabilities, ",")
}

// AgentTransport wraps an http.RoundTripper and injects agent identification headers.
type AgentTransport struct {
	Base http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t *AgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Clone request to avoid mutating the original
	clone := req.Clone(req.Context())

	clone.Header.Set("X-Agent-Version", version.Version)
	clone.Header.Set("X-Agent-OS", runtime.GOOS)
	clone.Header.Set("X-Agent-Arch", runtime.GOARCH)
	clone.Header.Set(CapabilitiesHeader, CapabilityList())

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// NewClient returns an *http.Client configured with AgentTransport over the
// shared control-plane transport and the specified timeout.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &AgentTransport{Base: ControlPlaneTransport()},
		Timeout:   timeout,
	}
}
