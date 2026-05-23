package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestStartWebSocketClientIfEnabled_DisabledNoops(t *testing.T) {
	t.Setenv("HOSTLINK_WS_ENABLED", "")
	called := false

	started := startWebSocketClientIfEnabled(context.Background(), func() (webSocketRuntime, error) {
		called = true
		return &fakeWebSocketRuntime{}, nil
	})

	if started {
		t.Fatal("expected websocket startup to be skipped")
	}
	if called {
		t.Fatal("expected constructor not to be called when websocket is disabled")
	}
}

func TestStartWebSocketClientIfEnabled_EnabledStartsAsync(t *testing.T) {
	t.Setenv("HOSTLINK_WS_ENABLED", "true")
	startedCh := make(chan struct{})
	releaseCh := make(chan struct{})
	runtime := &fakeWebSocketRuntime{startedCh: startedCh, releaseCh: releaseCh}

	started := startWebSocketClientIfEnabled(context.Background(), func() (webSocketRuntime, error) {
		return runtime, nil
	})

	if !started {
		t.Fatal("expected websocket startup to be attempted")
	}
	select {
	case <-startedCh:
	case <-time.After(time.Second):
		t.Fatal("expected websocket client to start asynchronously")
	}
	close(releaseCh)
}

func TestStartWebSocketClientIfEnabled_ConstructorFailureDoesNotStart(t *testing.T) {
	t.Setenv("HOSTLINK_WS_ENABLED", "true")

	started := startWebSocketClientIfEnabled(context.Background(), func() (webSocketRuntime, error) {
		return nil, errors.New("missing agent state")
	})

	if started {
		t.Fatal("expected websocket startup to report not started after constructor failure")
	}
}

type fakeWebSocketRuntime struct {
	startedCh chan struct{}
	releaseCh chan struct{}
}

func (f *fakeWebSocketRuntime) Start(ctx context.Context) error {
	if f.startedCh != nil {
		close(f.startedCh)
	}
	if f.releaseCh != nil {
		select {
		case <-f.releaseCh:
		case <-ctx.Done():
		}
	}
	return nil
}
