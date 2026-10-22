package apiserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"hostlink/internal/httpclient"
	"hostlink/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// controlPlaneServer is an HTTP/2 control-plane test server.
type controlPlaneServer struct {
	url         string
	certPool    *x509.CertPool
	connections atomic.Int64
}

// newControlPlaneServer starts an HTTP/2 test server.
//
// When stallFirstConnection is true the server behaves like a control plane
// whose first connection stopped answering: the requests on that connection
// never get a response, while the requests on the connections opened later
// are answered. A dead connection is the case the agent has to survive.
//
// delay is the time the server takes to answer a request.
func newControlPlaneServer(t *testing.T, stallFirstConnection bool, delay time.Duration) *controlPlaneServer {
	t.Helper()

	var mu sync.Mutex
	var stalledAddr string
	release := make(chan struct{})

	controlPlane := &controlPlaneServer{}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if stalledAddr == "" {
			stalledAddr = r.RemoteAddr
		}
		addr := r.RemoteAddr
		mu.Unlock()

		if stallFirstConnection && addr == stalledAddr {
			<-release
			return
		}

		if delay > 0 {
			time.Sleep(delay)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message":"ok"}`))
	})

	server := httptest.NewUnstartedServer(handler)
	server.EnableHTTP2 = true
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			controlPlane.connections.Add(1)
		}
	}
	server.StartTLS()

	t.Cleanup(func() {
		close(release)
		server.Close()
	})

	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())

	controlPlane.url = server.URL
	controlPlane.certPool = pool

	return controlPlane
}

// newTestClientWithTransport builds a client that uses the given transport.
func newTestClientWithTransport(t *testing.T, serverURL string, timeout, threshold time.Duration, transport http.RoundTripper) *client {
	t.Helper()

	stateDir, keyPath := testutil.SetupAgentIdentity(t)

	c, err := NewClient(Config{
		baseURL:              serverURL,
		privateKeyPath:       keyPath,
		agentStatePath:       stateDir,
		timeout:              timeout,
		slowRequestThreshold: threshold,
		transport:            transport,
	})
	require.NoError(t, err)

	return c
}

// newTestTransport returns a control-plane transport that trusts the test
// server certificate.
func newTestTransport(t *testing.T, server *controlPlaneServer) *httpclient.ControlPlaneTransport {
	t.Helper()

	return httpclient.NewControlPlaneTransport(httpclient.ControlPlaneSettings{
		PingInterval:           time.Second,
		PingTimeout:            time.Second,
		MaxConsecutiveTimeouts: 1,
		TLSConfig:              &tls.Config{RootCAs: server.certPool},
	})
}

func TestClient_DeadConnection_NextRequestGoesOutOnAFreshConnection(t *testing.T) {
	server := newControlPlaneServer(t, true, 0)
	transport := newTestTransport(t, server)

	const timeout = 500 * time.Millisecond
	c := newTestClientWithTransport(t, server.url, timeout, time.Second, transport)

	err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
	require.Error(t, err, "the request on the dead connection must fail")

	started := time.Now()
	err = c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
	elapsed := time.Since(started)

	require.NoError(t, err, "the next request must go out on a fresh connection")
	assert.Less(t, elapsed, 5*time.Second, "the next request must not wait for the client timeout")
	assert.Equal(t, int64(2), server.connections.Load(), "the dead connection must be replaced")
}

func TestClient_HealthyConnection_IsReused(t *testing.T) {
	server := newControlPlaneServer(t, false, 0)
	transport := newTestTransport(t, server)

	c := newTestClientWithTransport(t, server.url, 5*time.Second, time.Second, transport)

	for i := 0; i < 3; i++ {
		require.NoError(t, c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil))
	}

	assert.Equal(t, int64(1), server.connections.Load(), "a healthy connection must be reused")
}

func TestClient_DeadConnection_LogsTheDrop(t *testing.T) {
	server := newControlPlaneServer(t, true, 0)
	transport := newTestTransport(t, server)

	const timeout = 500 * time.Millisecond
	c := newTestClientWithTransport(t, server.url, timeout, time.Second, transport)

	output := testutil.CaptureLogs(t, func() {
		err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
		require.Error(t, err)
	})

	assert.Contains(t, output, "control-plane connection dropped")
}

// The connection is dropped when the limit of consecutive timeouts is
// reached, not before.
func TestClient_ConsecutiveTimeouts_DropTheConnectionOnTheLimit(t *testing.T) {
	server := newControlPlaneServer(t, true, 0)
	transport := httpclient.NewControlPlaneTransport(httpclient.ControlPlaneSettings{
		PingInterval:           time.Second,
		PingTimeout:            time.Second,
		MaxConsecutiveTimeouts: 2,
		TLSConfig:              &tls.Config{RootCAs: server.certPool},
	})

	const timeout = 300 * time.Millisecond
	c := newTestClientWithTransport(t, server.url, timeout, time.Second, transport)

	err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
	require.Error(t, err)
	assert.Equal(t, int64(1), server.connections.Load(), "the connection must be kept below the limit")

	err = c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
	require.Error(t, err)
	assert.Equal(t, int64(1), server.connections.Load(), "the dead connection is still in use")

	err = c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
	require.NoError(t, err, "the request after the limit must go out on a fresh connection")
	assert.Equal(t, int64(2), server.connections.Load(), "the connection must be dropped on the limit")
}

// A caller that gives up on a request is not a connection problem: the
// connection is healthy and must be kept.
func TestClient_CallerDeadline_KeepsTheConnection(t *testing.T) {
	server := newControlPlaneServer(t, false, 300*time.Millisecond)
	transport := newTestTransport(t, server)

	c := newTestClientWithTransport(t, server.url, 5*time.Second, time.Second, transport)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	output := testutil.CaptureLogs(t, func() {
		err := c.Get(ctx, "/api/v1/agents/test-agent-123/heartbeat", nil)
		require.Error(t, err)
	})

	assert.NotContains(t, output, "control-plane connection dropped",
		"a caller that gave up must not cost the connection")
	assert.Equal(t, int64(1), server.connections.Load(), "the healthy connection must be kept")
}

// A timed-out request reports a deadline error, so the callers that check
// for it keep working.
func TestClient_Timeout_ReportsADeadlineError(t *testing.T) {
	server := newControlPlaneServer(t, true, 0)
	transport := newTestTransport(t, server)

	c := newTestClientWithTransport(t, server.url, 300*time.Millisecond, time.Second, transport)

	err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)

	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Contains(t, err.Error(), "timed out")

	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	assert.True(t, netErr.Timeout(), "a timed-out request must report a timeout error")
}

// The connection that stops answering without failing is closed by the
// health check, so the next request goes out on a fresh connection.
func TestClient_SilentConnection_IsClosedByTheHealthCheck(t *testing.T) {
	server := newControlPlaneServer(t, false, 0)
	proxy := newPausingProxy(t, server.url)

	const (
		pingInterval = 300 * time.Millisecond
		pingTimeout  = 300 * time.Millisecond
	)

	transport := httpclient.NewControlPlaneTransport(httpclient.ControlPlaneSettings{
		PingInterval: pingInterval,
		PingTimeout:  pingTimeout,
		// The limit is high enough that the timeout path cannot be the one
		// that closes the connection.
		MaxConsecutiveTimeouts: 10,
		TLSConfig:              &tls.Config{RootCAs: server.certPool},
	})

	const timeout = 3 * time.Second
	c := newTestClientWithTransport(t, proxy.url(), timeout, time.Second, transport)

	require.NoError(t, c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil))

	// The connection stops answering without being closed.
	proxy.pause()

	// The health check pings the silent connection and closes it when the
	// ping goes unanswered.
	time.Sleep(pingInterval + pingTimeout + 500*time.Millisecond)

	started := time.Now()
	err := c.Get(context.Background(), "/api/v1/agents/test-agent-123/heartbeat", nil)
	elapsed := time.Since(started)

	require.NoError(t, err, "the next request must go out on a fresh connection")
	assert.Less(t, elapsed, timeout, "the next request must not wait for the client timeout")
	assert.Equal(t, int64(2), server.connections.Load(), "the silent connection must be closed")
}

// pausingProxy forwards TCP connections to a target and can stop forwarding
// the traffic of the connection it opened first. The peer then sees a
// connection that went silent, without the connection being closed.
type pausingProxy struct {
	listener net.Listener
	target   string
	conns    atomic.Int64
	paused   atomic.Bool
	stop     chan struct{}
	stopOnce sync.Once
}

// newPausingProxy starts a proxy in front of the given target URL.
func newPausingProxy(t *testing.T, targetURL string) *pausingProxy {
	t.Helper()

	address := strings.TrimPrefix(targetURL, "https://")

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	proxy := &pausingProxy{listener: listener, target: address, stop: make(chan struct{})}
	t.Cleanup(func() {
		proxy.close()
	})

	go proxy.serve()

	return proxy
}

// url returns the address the proxy listens on.
func (p *pausingProxy) url() string {
	return "https://" + p.listener.Addr().String()
}

// pause stops the forwarding of the traffic.
func (p *pausingProxy) pause() {
	p.paused.Store(true)
}

// close stops the proxy and the connections it holds.
func (p *pausingProxy) close() {
	p.stopOnce.Do(func() {
		close(p.stop)
		_ = p.listener.Close()
	})
}

func (p *pausingProxy) serve() {
	for {
		client, err := p.listener.Accept()
		if err != nil {
			return
		}

		go p.handle(client)
	}
}

func (p *pausingProxy) handle(client net.Conn) {
	server, err := net.Dial("tcp", p.target)
	if err != nil {
		_ = client.Close()
		return
	}

	// Only the first connection can be paused: the client has to be able to
	// open a new one.
	pausable := p.conns.Add(1) == 1

	go p.pump(server, client, pausable)
	go p.pump(client, server, pausable)
}

// pump copies the traffic of one direction. While the connection is paused
// the traffic stops, so the peer sees a silent connection.
func (p *pausingProxy) pump(dst, src net.Conn, pausable bool) {
	defer func() {
		_ = dst.Close()
		_ = src.Close()
	}()

	buffer := make([]byte, 32*1024)

	for {
		// While the connection is paused the traffic stops, so the peer sees a
		// silent connection.
		for pausable && p.paused.Load() {
			select {
			case <-p.stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
		}

		select {
		case <-p.stop:
			return
		default:
		}

		read, err := src.Read(buffer)
		if read > 0 {
			if _, writeErr := dst.Write(buffer[:read]); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
