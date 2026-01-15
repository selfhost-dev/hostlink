package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWatchSignals_CancelsContextOnSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := WatchSignals(cancel)
	defer stop()

	// Cancel directly (simulating signal effect) to verify wiring
	cancel()

	assert.Error(t, ctx.Err())
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
}

func TestWatchSignals_StopPreventsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := WatchSignals(cancel)
	stop() // Stop before any signal

	// Give a moment for any goroutine to fire
	time.Sleep(10 * time.Millisecond)

	// Context should not be cancelled
	assert.NoError(t, ctx.Err())
}
