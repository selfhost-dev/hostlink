package metricsjob

import (
	"context"
	"fmt"
	"time"

	"github.com/labstack/gommon/log"
)

// TriggerConfig holds configuration for the Trigger function
type TriggerConfig struct {
	InitialDelay time.Duration
	SleepFunc    func(time.Duration)
}

// DefaultTriggerConfig returns the default configuration
func DefaultTriggerConfig() TriggerConfig {
	return TriggerConfig{
		InitialDelay: 20 * time.Second,
		SleepFunc:    time.Sleep,
	}
}

func triggerWithConfig(ctx context.Context, fn func() error, config TriggerConfig) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(config.InitialDelay):
			if err := safeCall(fn); err != nil {
				log.Errorf("Failed while running metrics job: %s", err)
			}
		}
	}
}

func safeCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Panic recovered in metrics: %v", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn()
}

func TriggerWithConfig(ctx context.Context, fn func() error, config TriggerConfig) {
	triggerWithConfig(ctx, fn, config)
}

func Trigger(ctx context.Context, fn func() error) {
	triggerWithConfig(ctx, fn, DefaultTriggerConfig())
}
