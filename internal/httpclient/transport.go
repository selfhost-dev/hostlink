// Package httpclient provides HTTP client utilities with agent identification headers.
package httpclient

import (
	"net/http"
	"runtime"
	"time"

	"hostlink/version"
)

// Capabilities the control plane may rely on, sent on every agent request (CapabilitiesHeader).
// sealed-env-v1: the agent opens a task's sealed env (selfhost #3168).
const (
	CapabilitiesHeader = "X-Agent-Capabilities"
	Capabilities       = "sealed-env-v1"
)

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
	clone.Header.Set(CapabilitiesHeader, Capabilities)

	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

// NewClient returns an *http.Client configured with AgentTransport and the specified timeout.
func NewClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &AgentTransport{},
		Timeout:   timeout,
	}
}
