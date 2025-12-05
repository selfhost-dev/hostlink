package heartbeatjob

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockHeartbeatService struct {
	mock.Mock
}

func (m *MockHeartbeatService) Send() error {
	args := m.Called()
	return args.Error(0)
}

func immediateTrigger(callCount int, done chan struct{}) TriggerFunc {
	return func(ctx context.Context, fn func() error) {
		for i := 0; i < callCount; i++ {
			fn()
		}
		close(done)
		<-ctx.Done()
	}
}

// TestNew_DefaultsTrigger - New() uses default trigger
func TestNew_DefaultsTrigger(t *testing.T) {
	job := New()

	assert.NotNil(t, job.config.Trigger)
}

// TestNewWithConfig_UsesCustomTrigger - custom trigger is used when provided
func TestNewWithConfig_UsesCustomTrigger(t *testing.T) {
	customTriggerCalled := false
	customTrigger := func(ctx context.Context, fn func() error) {
		customTriggerCalled = true
		<-ctx.Done()
	}

	job := NewWithConfig(HeartbeatJobConfig{
		Trigger: customTrigger,
	})

	svc := new(MockHeartbeatService)
	ctx := context.Background()
	cancel := job.Register(ctx, svc)
	cancel()
	job.Shutdown()

	assert.True(t, customTriggerCalled)
}

// TestNewWithConfig_DefaultsNilTrigger - nil trigger defaults to Trigger
func TestNewWithConfig_DefaultsNilTrigger(t *testing.T) {
	job := NewWithConfig(HeartbeatJobConfig{
		Trigger: nil,
	})

	assert.NotNil(t, job.config.Trigger)
}

// TestRegister_CallsServiceSend - trigger calls heartbeat.Service.Send()
func TestRegister_CallsServiceSend(t *testing.T) {
	svc := new(MockHeartbeatService)
	svc.On("Send").Return(nil).Times(3)

	done := make(chan struct{})
	job := NewWithConfig(HeartbeatJobConfig{
		Trigger: immediateTrigger(3, done),
	})

	ctx := context.Background()
	cancel := job.Register(ctx, svc)
	<-done
	cancel()
	job.Shutdown()

	svc.AssertNumberOfCalls(t, "Send", 3)
}

// TestRegister_ContinuesOnError - job continues running after Send() error
func TestRegister_ContinuesOnError(t *testing.T) {
	svc := new(MockHeartbeatService)
	svc.On("Send").Return(errors.New("connection refused")).Times(3)

	done := make(chan struct{})
	job := NewWithConfig(HeartbeatJobConfig{
		Trigger: immediateTrigger(3, done),
	})

	ctx := context.Background()
	cancel := job.Register(ctx, svc)
	<-done
	cancel()
	job.Shutdown()

	svc.AssertNumberOfCalls(t, "Send", 3)
}

// TestRegister_ReturnsCancel - returns cancel function
func TestRegister_ReturnsCancel(t *testing.T) {
	svc := new(MockHeartbeatService)

	job := NewWithConfig(HeartbeatJobConfig{
		Trigger: func(ctx context.Context, fn func() error) {
			<-ctx.Done()
		},
	})

	ctx := context.Background()
	cancel := job.Register(ctx, svc)

	assert.NotNil(t, cancel)
	cancel()
	job.Shutdown()
}

// TestShutdown_StopsJob - Shutdown() stops the job gracefully
func TestShutdown_StopsJob(t *testing.T) {
	var callCount atomic.Int32
	svc := new(MockHeartbeatService)
	svc.On("Send").Return(nil).Run(func(args mock.Arguments) {
		callCount.Add(1)
	})

	job := NewWithConfig(HeartbeatJobConfig{
		Trigger: func(ctx context.Context, fn func() error) {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					fn()
					time.Sleep(1 * time.Millisecond)
				}
			}
		},
	})

	ctx := context.Background()
	job.Register(ctx, svc)

	time.Sleep(10 * time.Millisecond)
	countBeforeShutdown := callCount.Load()
	job.Shutdown()

	time.Sleep(10 * time.Millisecond)
	countAfterShutdown := callCount.Load()

	assert.Equal(t, countBeforeShutdown, countAfterShutdown)
}

// TestShutdown_WaitsForCompletion - Shutdown() waits for goroutine to finish
func TestShutdown_WaitsForCompletion(t *testing.T) {
	var wg sync.WaitGroup
	var goroutineFinished atomic.Bool

	svc := new(MockHeartbeatService)

	job := NewWithConfig(HeartbeatJobConfig{
		Trigger: func(ctx context.Context, fn func() error) {
			<-ctx.Done()
			time.Sleep(20 * time.Millisecond)
			goroutineFinished.Store(true)
		},
	})

	ctx := context.Background()
	job.Register(ctx, svc)

	wg.Add(1)
	go func() {
		defer wg.Done()
		job.Shutdown()
	}()

	wg.Wait()
	assert.True(t, goroutineFinished.Load())
}
