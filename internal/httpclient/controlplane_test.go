package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// deadlineError is a context that expires at the given moment.
func deadlineContext(t *testing.T, in time.Duration) context.Context {
	t.Helper()

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(in))
	t.Cleanup(cancel)

	return ctx
}

func TestClientTimedOut_ClientTimeout(t *testing.T) {
	// The client gave up: the request ran into the deadline of the client.
	ctx := deadlineContext(t, -time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://control-plane.test/x", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	assert.True(t, clientTimedOut(req, context.DeadlineExceeded))
}

func TestClientTimedOut_ClientCancellation(t *testing.T) {
	// The client cancels the request when its own timeout expires.
	req, err := http.NewRequest(http.MethodGet, "https://control-plane.test/x", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	cancel := make(chan struct{})
	req.Cancel = cancel
	close(cancel)

	assert.True(t, clientTimedOut(req, errors.New("net/http: request canceled")))
}

func TestClientTimedOut_CallerDeadline(t *testing.T) {
	// The caller gave up before the client could.
	ctx := WithCallerDeadline(deadlineContext(t, -time.Second))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://control-plane.test/x", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	assert.False(t, clientTimedOut(req, context.DeadlineExceeded),
		"a request the caller abandoned is not a connection problem")
}

func TestClientTimedOut_TransportTimeout(t *testing.T) {
	// The connection was never established: a handshake timeout says nothing
	// about the pooled connection.
	req, err := http.NewRequest(http.MethodGet, "https://control-plane.test/x", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	handshakeTimeout := &net.OpError{
		Op:  "remote error",
		Err: os.ErrDeadlineExceeded,
	}

	assert.False(t, clientTimedOut(req, handshakeTimeout))
}

func TestClientTimedOut_ConnectionRefused(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "https://control-plane.test/x", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	refused := &net.OpError{
		Op:  "dial",
		Err: errors.New("connection refused"),
	}

	assert.False(t, clientTimedOut(req, refused))
}
