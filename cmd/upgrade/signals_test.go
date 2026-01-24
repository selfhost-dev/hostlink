package upgrade

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWatchSignals_CancelsContextOnSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := WatchSignals(cancel)
	defer stop()

	// Send SIGINT to ourselves
	proc, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, proc.Signal(syscall.SIGINT))

	// Wait for cancellation
	select {
	case <-ctx.Done():
		assert.ErrorIs(t, ctx.Err(), context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("context was not cancelled within timeout")
	}
}

func TestWatchSignals_StopPreventsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := WatchSignals(cancel)
	stop() // Stop before any signal

	// Give goroutine time to exit
	time.Sleep(10 * time.Millisecond)

	// Context should not be cancelled
	assert.NoError(t, ctx.Err())
}

func TestWatchSignals_MultipleCallsAreIndependent(t *testing.T) {
	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	stop1 := WatchSignals(cancel1)
	stop2 := WatchSignals(cancel2)
	defer stop2()

	// Stop only the first watcher
	stop1()

	time.Sleep(10 * time.Millisecond)

	// Neither context should be cancelled
	assert.NoError(t, ctx1.Err())
	assert.NoError(t, ctx2.Err())
}
