package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/labstack/gommon/log"

	"hostlink/config/appconf"
)

// ControlPlaneSettings are the dead-connection detection settings of the
// control-plane transport.
type ControlPlaneSettings struct {
	// PingInterval is how long a connection may stay silent before the
	// transport health-checks it with an HTTP/2 ping.
	PingInterval time.Duration
	// PingTimeout is how long the transport waits for the answer to the
	// health check before it closes the connection.
	PingTimeout time.Duration
	// MaxConsecutiveTimeouts is how many consecutive request timeouts are
	// allowed before the connection is dropped and the next request dials a
	// fresh one.
	MaxConsecutiveTimeouts int
	// TLSConfig is the TLS configuration of the connection. Nil means the
	// defaults of the platform.
	TLSConfig *tls.Config
}

// ControlPlaneTransport is the HTTP transport of the control-plane clients.
//
// A control-plane connection can die without the client noticing: the
// connection stays open, the requests go out and no answer comes back, so
// every call waits for its own client timeout. HTTP/2 keeps such a connection
// in the pool, so the next call waits for the full timeout as well.
//
// The transport therefore watches the connection in two ways: it health-checks
// an idle connection with an HTTP/2 ping and closes the connection when the
// ping goes unanswered, and it drops the connection after a configurable
// number of consecutive request timeouts, so the next request dials a fresh
// one.
//
// A request that ran out of time fails with an error that reports a
// deadline: the callers that check for context.DeadlineExceeded keep working.
// A request that fails because the caller imposed a deadline of its own is
// left alone: the connection may be healthy, and dropping it would cost an
// extra connection for nothing.
type ControlPlaneTransport struct {
	settings ControlPlaneSettings

	mu       sync.Mutex
	base     *http.Transport
	timeouts int
}

// NewControlPlaneTransport returns a transport for control-plane calls.
func NewControlPlaneTransport(settings ControlPlaneSettings) *ControlPlaneTransport {
	if settings.MaxConsecutiveTimeouts < 1 {
		settings.MaxConsecutiveTimeouts = 1
	}

	return &ControlPlaneTransport{
		settings: settings,
		base:     newControlPlaneHTTPTransport(settings),
	}
}

var (
	sharedOnce      sync.Once
	sharedTransport *ControlPlaneTransport
)

// SharedControlPlaneTransport returns the transport shared by every
// control-plane client, so that one redial recovers all of them.
func SharedControlPlaneTransport() *ControlPlaneTransport {
	sharedOnce.Do(func() {
		sharedTransport = NewControlPlaneTransport(ControlPlaneSettings{
			PingInterval:           appconf.APIPingInterval(),
			PingTimeout:            appconf.APIPingTimeout(),
			MaxConsecutiveTimeouts: appconf.APIMaxConsecutiveTimeouts(),
		})
	})

	return sharedTransport
}

// NewControlPlaneClient returns an *http.Client for control-plane calls: it
// sends the agent identification headers and uses the shared control-plane
// transport, so a dead connection is dropped once for every client.
func NewControlPlaneClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &AgentTransport{Base: SharedControlPlaneTransport()},
		Timeout:   timeout,
	}
}

// RoundTrip implements http.RoundTripper.
func (t *ControlPlaneTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.current()

	resp, err := base.RoundTrip(req)
	if err == nil {
		t.resetTimeouts(base)
		return resp, nil
	}

	// A request that ran out of time on the connection means the connection
	// is stuck, so it is dropped and the next request dials a fresh one. A
	// request that failed for another reason, a failed dial or a failed
	// handshake, says nothing about the pooled connection and is left alone.
	if !clientTimedOut(req, err) {
		return resp, err
	}

	t.recordTimeout(base)

	return resp, newTimeoutError(err)
}

// current returns the transport that serves the requests.
func (t *ControlPlaneTransport) current() *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.base
}

// recordTimeout counts a consecutive timeout and drops the connection when
// the limit is reached.
//
// The transport that served the request is passed in: a timeout of a
// connection that was already dropped must not drop the connection that
// replaced it.
//
// The connection pool is replaced instead of closed, so the next request
// dials a fresh connection: a connection that was still busy when the timeout
// was counted would be handed out again otherwise.
func (t *ControlPlaneTransport) recordTimeout(base *http.Transport) {
	t.mu.Lock()

	if t.base != base {
		t.mu.Unlock()
		return
	}

	t.timeouts++
	if t.timeouts < t.settings.MaxConsecutiveTimeouts {
		t.mu.Unlock()
		return
	}

	old := t.base
	t.base = newControlPlaneHTTPTransport(t.settings)
	count := t.timeouts
	t.timeouts = 0
	t.mu.Unlock()

	// The timed-out request is still registered on the old connection when
	// the timeout is counted, so the connection is closed a second time once
	// the request is cleaned up. A connection that stays busy is closed by
	// the idle timeout of the transport.
	old.CloseIdleConnections()
	time.AfterFunc(time.Second, old.CloseIdleConnections)

	log.Warnf("control-plane connection dropped: %d consecutive timeouts, redialing", count)
}

// resetTimeouts forgets the timeouts that were not followed by a successful
// request. A successful request means the connection works, so the streak of
// another loop does not matter.
func (t *ControlPlaneTransport) resetTimeouts(base *http.Transport) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.base != base {
		return
	}

	t.timeouts = 0
}

// callerDeadlineKey marks the deadline a caller imposed on a control-plane
// request.
type callerDeadlineKey struct{}

// WithCallerDeadline returns a context that carries the deadline the caller
// imposed, so the transport can tell a caller deadline from the timeout of
// the client. Callers that pass their own deadline to a control-plane request
// must go through it.
//
// A deadline of the caller that is shorter than the timeout of the client
// hands the recovery of a stuck connection to the health check: the request
// is abandoned before the client can tell that the connection is stuck.
func WithCallerDeadline(ctx context.Context) context.Context {
	deadline, ok := ctx.Deadline()
	if !ok {
		return ctx
	}

	return context.WithValue(ctx, callerDeadlineKey{}, deadline)
}

// clientTimedOut reports whether the request ran out of time because the
// client gave up on it.
//
// The client cancels the request when its own timeout expires. A request that
// the caller abandoned is not a connection problem: the caller gave up, the
// connection may well be healthy, and dropping it would cost an extra
// connection for nothing.
func clientTimedOut(req *http.Request, err error) bool {
	ctx := req.Context()

	// The caller imposed a deadline of its own. It is the binding constraint
	// when it is not later than the deadline of the client: then the caller
	// gave up, not the client.
	if caller, ok := ctx.Value(callerDeadlineKey{}).(time.Time); ok {
		effective, hasEffective := ctx.Deadline()
		if hasEffective && !effective.Before(caller) {
			return false
		}
	}

	// The client cancels the request when its own timeout expires.
	if req.Cancel != nil {
		select {
		case <-req.Cancel:
			return true
		default:
		}
	}

	// The request ran into the deadline of the client. A timeout of the
	// transport itself, a failed dial or a failed handshake, is not one: it
	// says nothing about the pooled connection.
	return errors.Is(err, context.DeadlineExceeded)
}

// newTimeoutError returns the error a control-plane request that ran out of
// time reports. The message is the same for every timeout, so the journal
// does not depend on which cancellation the request hit first. The error that
// ended the request stays in the chain, so the cause is not lost.
func newTimeoutError(orig error) error {
	return &timeoutError{cause: context.DeadlineExceeded, orig: orig}
}

// timeoutError is the error of a request that ran out of time.
type timeoutError struct {
	cause error
	orig  error
}

func (e *timeoutError) Error() string {
	return "control-plane request timed out: " + e.cause.Error()
}

func (e *timeoutError) Unwrap() []error {
	return []error{e.cause, e.orig}
}

func (e *timeoutError) Timeout() bool {
	return true
}

func (e *timeoutError) Temporary() bool {
	return true
}

// newControlPlaneHTTPTransport returns the HTTP transport of the control-plane
// clients: it negotiates HTTP/2, so the connection can be health-checked with
// a ping, and it keeps a small pool of idle connections.
func newControlPlaneHTTPTransport(settings ControlPlaneSettings) *http.Transport {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)

	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	// The TLS configuration is copied for every transport: net/http writes the
	// negotiated protocols into it, and the transports run in parallel.
	var tlsConfig *tls.Config
	if settings.TLSConfig != nil {
		tlsConfig = settings.TLSConfig.Clone()
	}

	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
		Protocols:             protocols,
		HTTP2: &http.HTTP2Config{
			// A connection that stays silent is health-checked with a ping,
			// and a ping that goes unanswered closes the connection. The
			// health check needs HTTP/2, so it only runs against a control
			// plane that speaks it over TLS.
			SendPingTimeout: settings.PingInterval,
			PingTimeout:     settings.PingTimeout,
		},
	}
}
