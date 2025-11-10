package metricsjob

import (
	"context"
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
			if err := fn(); err != nil {
				log.Errorf("Failed while running metrics job: %s", err)
			}
		}
	}
}

func Trigger(ctx context.Context, fn func() error) {
	triggerWithConfig(ctx, fn, DefaultTriggerConfig())
}
