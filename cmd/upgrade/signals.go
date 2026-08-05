package upgrade

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

// maxGoroutineDumpBytes caps the goroutine stack dump size.
const maxGoroutineDumpBytes = 1 << 20 // 1 MiB

// dumpGoroutineStacks writes all goroutine stacks to w. It is a package-level
// variable so tests can observe or replace it.
var dumpGoroutineStacks = func(w io.Writer) {
	buf := make([]byte, maxGoroutineDumpBytes)
	n := runtime.Stack(buf, true) // all goroutines, not just the current one
	fmt.Fprintf(w, "SIGUSR1 received: dumping all goroutine stacks\n%s\n", buf[:n])
}

// WatchSIGUSR1 listens for SIGUSR1 and writes a full goroutine stack dump to w
// (wedge diagnosis: send kill -USR1 <pid> and inspect the output).
// The process keeps running — the dump is purely diagnostic.
// Returns a cleanup function that stops signal watching.
func WatchSIGUSR1(w io.Writer) (stop func()) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGUSR1)

	go func() {
		for range sigChan {
			dumpGoroutineStacks(w)
		}
	}()

	return func() {
		signal.Stop(sigChan)
		close(sigChan)
	}
}

// WatchSignals listens for SIGTERM/SIGINT and cancels the given context.
// Returns a cleanup function that stops signal watching.
func WatchSignals(cancel context.CancelFunc) (stop func()) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		_, ok := <-sigChan
		if ok {
			cancel()
		}
	}()

	return func() {
		signal.Stop(sigChan)
		close(sigChan)
	}
}
