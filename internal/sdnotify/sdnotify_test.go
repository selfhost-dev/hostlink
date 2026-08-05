package sdnotify

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listenSocket creates a unixgram socket at a temp path and returns it.
func listenSocket(t *testing.T) (*net.UnixConn, string) {
	t.Helper()
	// Short path: macOS unix socket paths are limited to ~104 bytes and
	// t.TempDir() names embed the (long) test name.
	dir, err := os.MkdirTemp("", "sdnotify")
	require.NoError(t, err)
	sockPath := filepath.Join(dir, "s.sock")
	addr := &net.UnixAddr{Name: sockPath, Net: "unixgram"}
	ln, err := net.ListenUnixgram("unixgram", addr)
	require.NoError(t, err)
	t.Cleanup(func() {
		ln.Close()
		os.RemoveAll(dir)
	})
	return ln, sockPath
}

func readMessage(t *testing.T, ln *net.UnixConn) string {
	t.Helper()
	require.NoError(t, ln.SetReadDeadline(time.Now().Add(2*time.Second)))
	buf := make([]byte, 256)
	n, _, err := ln.ReadFromUnix(buf)
	require.NoError(t, err)
	return string(buf[:n])
}

func TestStartWatchdog_SendsKeepalivesOnInterval(t *testing.T) {
	ln, sockPath := listenSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)
	t.Setenv("WATCHDOG_USEC", "20000") // 20ms watchdog interval -> 10ms keepalive tick

	stop := StartWatchdog()
	defer stop()

	assert.Equal(t, "WATCHDOG=1", readMessage(t, ln))
}

func TestStartWatchdog_StopStopsKeepalives(t *testing.T) {
	ln, sockPath := listenSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)
	t.Setenv("WATCHDOG_USEC", "20000")

	stop := StartWatchdog()
	// Consume the first keepalive
	readMessage(t, ln)

	stop()

	// No further messages should arrive after stop
	require.NoError(t, ln.SetReadDeadline(time.Now().Add(100*time.Millisecond)))
	buf := make([]byte, 256)
	_, _, err := ln.ReadFromUnix(buf)
	assert.Error(t, err, "expected no keepalives after stop")
}

func TestStartWatchdog_NoopWithoutEnv(t *testing.T) {
	t.Setenv("NOTIFY_SOCKET", "")
	t.Setenv("WATCHDOG_USEC", "")

	stop := StartWatchdog() // must not panic or block
	stop()
}

func TestNotifyReady_SendsReady(t *testing.T) {
	ln, sockPath := listenSocket(t)
	t.Setenv("NOTIFY_SOCKET", sockPath)

	NotifyReady()

	assert.Equal(t, "READY=1", readMessage(t, ln))
}
