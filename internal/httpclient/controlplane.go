package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"time"

	"github.com/labstack/gommon/log"

	"hostlink/config/appconf"
)

// The control-plane transport (selfhost#3963, hostlink#268). Every
// control-plane call shares one connection to the API edge, and the process
// default transport cannot tell when that connection is dead: on a stalled
// network path each call (heartbeat, tasks, credentials, metrics, self-update)
// waited out its full timeout on it, for minutes. This transport
//
//   - pings an HTTP/2 connection that has been silent for PingIdle and closes
//     it when the ping is not answered within PingTimeout;
//   - after RedialAfterTimeouts consecutive timeouts (an HTTP/1.1 edge, or a
//     stall pings do not catch), moves to a fresh connection pool, so the next
//     call dials fresh. Closing idle connections is not enough: an HTTP/2
//     connection whose cancelled streams were never acknowledged is not idle;
//   - logs both, and the first fresh connection after them.
//
// A healthy connection is left alone: pings only go out on a silent one.
// Per-request timeouts stay the caller's; there are no retries here, the job
// loops are the retry.

type ControlPlaneConfig struct {
	PingIdle            time.Duration // zero: no pings
	PingTimeout         time.Duration
	RedialAfterTimeouts int
}

// ControlPlaneConfigFromEnv reads the HOSTLINK_API_* settings (appconf).
func ControlPlaneConfigFromEnv() ControlPlaneConfig {
	return ControlPlaneConfig{
		PingIdle:            appconf.APIPingIdleTimeout(),
		PingTimeout:         appconf.APIPingTimeout(),
		RedialAfterTimeouts: appconf.APIRedialAfterTimeouts(),
	}
}

type controlPlaneTransport struct {
	base        atomic.Pointer[http.Transport]
	redialAfter int32
	timeouts    atomic.Int32
	troubled    atomic.Bool // a failed request since the last fresh connection
}

func newControlPlaneTransport(cfg ControlPlaneConfig, base *http.Transport) *controlPlaneTransport {
	base.HTTP2 = &http.HTTP2Config{SendPingTimeout: cfg.PingIdle, PingTimeout: cfg.PingTimeout}
	redialAfter := cfg.RedialAfterTimeouts
	if redialAfter < 1 {
		redialAfter = 1
	}
	t := &controlPlaneTransport{redialAfter: int32(redialAfter)}
	t.base.Store(base)
	return t
}

var shared struct {
	once      sync.Once
	transport *controlPlaneTransport
}

// ControlPlaneTransport is the one transport every control-plane client
// shares, so one redial serves every loop.
func ControlPlaneTransport() http.RoundTripper {
	shared.once.Do(func() {
		base := http.DefaultTransport.(*http.Transport).Clone()
		shared.transport = newControlPlaneTransport(ControlPlaneConfigFromEnv(), base)
	})
	return shared.transport
}

// NewControlPlaneClient returns a client on the shared control-plane
// transport with the caller's request timeout.
func NewControlPlaneClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: ControlPlaneTransport(), Timeout: timeout}
}

func (t *controlPlaneTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.troubled.Load() {
		req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
			GotConn: func(info httptrace.GotConnInfo) {
				if !info.Reused && t.troubled.CompareAndSwap(true, false) {
					log.Infoj(log.JSON{"event": "control_plane_reconnected", "host": req.URL.Host})
				}
			},
		}))
	}

	base := t.base.Load()
	resp, err := base.RoundTrip(req)
	if err == nil {
		t.timeouts.Store(0)
		return resp, nil
	}
	timedOut := isTimeout(req, err)
	// A caller's own cancel says nothing about the connection.
	if !timedOut && errors.Is(err, context.Canceled) {
		return nil, err
	}
	t.troubled.Store(true)
	if !timedOut {
		return nil, err
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		// http.Client cancels a wrapped transport's request through its Cancel
		// channel too; whichever fires first, report the deadline, as the
		// stdlib transport did ("context deadline exceeded").
		err = fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
	}

	if n := t.timeouts.Add(1); n >= t.redialAfter {
		t.timeouts.Store(0)
		// The pool the timeouts came from; a concurrent redial already swapped it.
		if t.base.CompareAndSwap(base, base.Clone()) {
			base.CloseIdleConnections()
			log.Warnj(log.JSON{"event": "control_plane_redial", "host": req.URL.Host, "consecutive_timeouts": n})
		}
	}
	return nil, err
}

func (t *controlPlaneTransport) CloseIdleConnections() { t.base.Load().CloseIdleConnections() }

// isTimeout: http.Client's own timer can cancel the request a moment before
// its context deadline passes, so the error may read "request canceled": a
// request whose deadline has passed timed out either way.
func isTimeout(req *http.Request, err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if deadline, ok := req.Context().Deadline(); ok && !time.Now().Before(deadline) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
