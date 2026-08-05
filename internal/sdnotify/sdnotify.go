// Package sdnotify implements systemd sd_notify integration:
// watchdog keepalives (WATCHDOG=1) and readiness signaling (READY=1).
//
// All functions are no-ops when NOTIFY_SOCKET is unset, so the agent runs
// unchanged outside systemd (development, containers, manual execution).
package sdnotify

import (
	"net"
	"os"
	"strconv"
	"time"
)

// StartWatchdog sends WATCHDOG=1 keepalives to $NOTIFY_SOCKET every half of
// the $WATCHDOG_USEC interval, keeping the systemd watchdog from killing the
// agent (unit: WatchdogSec=N). No-op when NOTIFY_SOCKET or WATCHDOG_USEC is
// unset. Returns a stop function that halts keepalives.
func StartWatchdog() (stop func()) {
	sock := os.Getenv("NOTIFY_SOCKET")
	usecRaw := os.Getenv("WATCHDOG_USEC")
	if sock == "" || usecRaw == "" {
		return func() {}
	}
	usec, err := strconv.ParseInt(usecRaw, 10, 64)
	if err != nil || usec <= 0 {
		return func() {}
	}

	interval := time.Duration(usec) * time.Microsecond / 2
	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				notify(sock, "WATCHDOG=1")
			}
		}
	}()

	return func() { close(stopCh) }
}

// NotifyReady sends READY=1 so systemd (Type=notify) considers the agent
// started. No-op when NOTIFY_SOCKET is unset.
func NotifyReady() {
	if sock := os.Getenv("NOTIFY_SOCKET"); sock != "" {
		notify(sock, "READY=1")
	}
}

func notify(sock, msg string) {
	conn, err := net.Dial("unixgram", sock)
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = conn.Write([]byte(msg))
}
