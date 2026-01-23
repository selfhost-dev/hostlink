package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

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
