package update

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockExecResult holds the result for a single command execution
type mockExecResult struct {
	output string
	err    error
}

// mockExecFunc creates an exec function that returns predefined results
func mockExecFunc(results ...mockExecResult) func(ctx context.Context, name string, args ...string) ([]byte, error) {
	idx := 0
	return func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if idx >= len(results) {
			return nil, errors.New("unexpected call to exec")
		}
		result := results[idx]
		idx++
		return []byte(result.output), result.err
	}
}

// recordingExecFunc records all calls made to the exec function
type recordingExec struct {
	calls   []execCall
	results []mockExecResult
	idx     int
}

type execCall struct {
	name string
	args []string
}

func newRecordingExec(results ...mockExecResult) *recordingExec {
	return &recordingExec{results: results}
}

func (r *recordingExec) exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.calls = append(r.calls, execCall{name: name, args: args})
	if r.idx >= len(r.results) {
		return nil, errors.New("unexpected call to exec")
	}
	result := r.results[r.idx]
	r.idx++
	return []byte(result.output), result.err
}

func TestServiceController_Stop_CallsSystemctl(t *testing.T) {
	recorder := newRecordingExec(mockExecResult{output: "", err: nil})
	sc := NewServiceController(ServiceConfig{
		ServiceName: "hostlink",
		ExecFunc:    recorder.exec,
	})

	err := sc.Stop(context.Background())

	require.NoError(t, err)
	require.Len(t, recorder.calls, 1)
	assert.Equal(t, "systemctl", recorder.calls[0].name)
	assert.Equal(t, []string{"stop", "hostlink"}, recorder.calls[0].args)
}

func TestServiceController_Stop_ReturnsErrorOnFailure(t *testing.T) {
	recorder := newRecordingExec(mockExecResult{
		output: "Failed to stop hostlink.service: Unit hostlink.service not loaded.",
		err:    errors.New("exit status 5"),
	})
	sc := NewServiceController(ServiceConfig{
		ServiceName: "hostlink",
		ExecFunc:    recorder.exec,
	})

	err := sc.Stop(context.Background())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to stop service")
}

func TestServiceController_Stop_RespectsTimeout(t *testing.T) {
	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	execCalled := false
	sc := NewServiceController(ServiceConfig{
		ServiceName: "hostlink",
		ExecFunc: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			execCalled = true
			// Check if context is done
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, nil
		},
	})

	err := sc.Stop(ctx)

	assert.True(t, execCalled)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestServiceController_Stop_HandlesAlreadyStopped(t *testing.T) {
	// When service is already stopped, systemctl stop still returns success
	recorder := newRecordingExec(mockExecResult{output: "", err: nil})
	sc := NewServiceController(ServiceConfig{
		ServiceName: "hostlink",
		ExecFunc:    recorder.exec,
	})

	err := sc.Stop(context.Background())

	require.NoError(t, err)
}

func TestServiceController_Stop_UsesConfiguredTimeout(t *testing.T) {
	var capturedCtx context.Context
	sc := NewServiceController(ServiceConfig{
		ServiceName: "hostlink",
		StopTimeout: 15 * time.Second,
		ExecFunc: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			capturedCtx = ctx
			return nil, nil
		},
	})

	_ = sc.Stop(context.Background())

	// Verify context has a deadline
	deadline, ok := capturedCtx.Deadline()
	require.True(t, ok, "context should have a deadline")
	// Deadline should be roughly 15 seconds from now (give some margin)
	assert.WithinDuration(t, time.Now().Add(15*time.Second), deadline, 2*time.Second)
}

func TestServiceController_Start_CallsSystemctl(t *testing.T) {
	recorder := newRecordingExec(mockExecResult{output: "", err: nil})
	sc := NewServiceController(ServiceConfig{
		ServiceName: "hostlink",
		ExecFunc:    recorder.exec,
	})

	err := sc.Start(context.Background())

	require.NoError(t, err)
	require.Len(t, recorder.calls, 1)
	assert.Equal(t, "systemctl", recorder.calls[0].name)
	assert.Equal(t, []string{"start", "hostlink"}, recorder.calls[0].args)
}

func TestServiceController_Start_ReturnsErrorOnFailure(t *testing.T) {
	recorder := newRecordingExec(mockExecResult{
		output: "Failed to start hostlink.service: Unit hostlink.service not found.",
		err:    errors.New("exit status 5"),
	})
	sc := NewServiceController(ServiceConfig{
		ServiceName: "hostlink",
		ExecFunc:    recorder.exec,
	})

	err := sc.Start(context.Background())

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to start service")
}

func TestServiceController_Start_RespectsTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sc := NewServiceController(ServiceConfig{
		ServiceName: "hostlink",
		ExecFunc: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, nil
		},
	})

	err := sc.Start(ctx)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestServiceController_Start_UsesConfiguredTimeout(t *testing.T) {
	var capturedCtx context.Context
	sc := NewServiceController(ServiceConfig{
		ServiceName:  "hostlink",
		StartTimeout: 20 * time.Second,
		ExecFunc: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			capturedCtx = ctx
			return nil, nil
		},
	})

	_ = sc.Start(context.Background())

	deadline, ok := capturedCtx.Deadline()
	require.True(t, ok, "context should have a deadline")
	assert.WithinDuration(t, time.Now().Add(20*time.Second), deadline, 2*time.Second)
}

func TestServiceController_DefaultTimeouts(t *testing.T) {
	sc := NewServiceController(ServiceConfig{
		ServiceName: "hostlink",
		ExecFunc:    func(ctx context.Context, name string, args ...string) ([]byte, error) { return nil, nil },
	})

	// Verify defaults are applied
	assert.Equal(t, 30*time.Second, sc.config.StopTimeout)
	assert.Equal(t, 30*time.Second, sc.config.StartTimeout)
}
