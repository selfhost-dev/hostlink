package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// selfhost#3963 / hostlink#268: on a stalled network path every control-plane
// call waited out its full timeout on the same dead connection, for minutes.
// The proxy black-holes the connections open at stall(), as that path did;
// connections dialled after it work.

func stallableServer(t *testing.T, cfg ControlPlaneConfig) (*http.Client, *blackholeProxy, string) {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(r.Proto))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(srv.Close)
	proxy := newBlackholeProxy(t, strings.TrimPrefix(srv.URL, "https://"))

	base := http.DefaultTransport.(*http.Transport).Clone()
	base.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport := newControlPlaneTransport(cfg, base)
	t.Cleanup(transport.CloseIdleConnections)
	return &http.Client{Transport: transport, Timeout: time.Second}, proxy, "https://" + proxy.addr() + "/"
}

func get(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	return string(body), err
}

func mustGet(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	body, err := get(client, url)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return body
}

func TestControlPlane_DeadConnectionIsReplacedWithinSeconds(t *testing.T) {
	client, proxy, url := stallableServer(t, ControlPlaneConfig{
		PingIdle: 200 * time.Millisecond, PingTimeout: 200 * time.Millisecond, RedialAfterTimeouts: 10,
	})
	if proto := mustGet(t, client, url); proto != "HTTP/2.0" {
		t.Fatalf("control plane spoke %s, want HTTP/2.0 (the proxy edge's protocol)", proto)
	}

	proxy.stall()
	_, _ = get(client, url) // lost with the connection

	start := time.Now()
	mustGet(t, client, url)
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Errorf("recovery took %s; the dead connection must be gone before the next call", took)
	}
	if got := proxy.connections(); got != 2 {
		t.Errorf("connections = %d, want 2 (the dead one, then one fresh dial)", got)
	}
}

func TestControlPlane_ConsecutiveTimeoutsDialFresh(t *testing.T) {
	// Pings off: the timeout count alone must get the agent off the dead
	// connection (an HTTP/1.1 edge, or pings that never trigger).
	client, proxy, url := stallableServer(t, ControlPlaneConfig{RedialAfterTimeouts: 2})
	mustGet(t, client, url)

	proxy.stall()
	for i := 0; i < 2; i++ {
		if _, err := get(client, url); err == nil {
			t.Fatalf("request %d on the stalled connection succeeded", i+1)
		}
	}

	mustGet(t, client, url)
	if got := proxy.connections(); got != 2 {
		t.Errorf("connections = %d, want 2", got)
	}
}

func TestControlPlane_HealthyConnectionIsReused(t *testing.T) {
	client, proxy, url := stallableServer(t, ControlPlaneConfig{
		PingIdle: 100 * time.Millisecond, PingTimeout: 200 * time.Millisecond, RedialAfterTimeouts: 1,
	})

	for i := 0; i < 3; i++ {
		mustGet(t, client, url)
		time.Sleep(300 * time.Millisecond) // idle past PingIdle: pings answered
	}

	if got := proxy.connections(); got != 1 {
		t.Errorf("connections = %d, want 1: a healthy connection is left alone", got)
	}
}

func TestControlPlaneClient_SharesOneTransport(t *testing.T) {
	a, b := NewControlPlaneClient(10*time.Second), NewControlPlaneClient(30*time.Second)

	if a.Transport != b.Transport {
		t.Error("control-plane clients must share one transport, so one redial serves every loop")
	}
	if a.Timeout != 10*time.Second || b.Timeout != 30*time.Second {
		t.Error("per-request timeouts must stay the caller's")
	}
}

func TestIsTimeout_CancelledPastItsDeadlineCounts(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Millisecond))
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.invalid/", nil)
	live, _ := http.NewRequest(http.MethodGet, "https://example.invalid/", nil)
	canceled := errors.New("net/http: request canceled")

	if !isTimeout(req, canceled) {
		t.Error("a request cancelled past its deadline timed out (http.Client's timer fired first)")
	}
	if isTimeout(live, canceled) {
		t.Error("a cancelled request with no deadline is not a timeout")
	}
}

func TestControlPlane_TimeoutReportsTheDeadline(t *testing.T) {
	client, proxy, url := stallableServer(t, ControlPlaneConfig{RedialAfterTimeouts: 10})
	mustGet(t, client, url)
	proxy.stall()

	for i := 0; i < 5; i++ {
		_, err := get(client, url)
		if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
			t.Fatalf("timeout %d reads %v; it must say the deadline passed, whichever cancel fired first", i+1, err)
		}
	}
}
