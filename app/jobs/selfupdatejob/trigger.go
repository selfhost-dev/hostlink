package selfupdatejob

import (
	"context"
	"fmt"
	"time"

	log "github.com/sirupsen/logrus"
)

// TriggerConfig holds configuration for the trigger.
type TriggerConfig struct {
	Interval time.Duration
}

// DefaultTriggerConfig returns the default trigger configuration (5 minute interval for debugging).
func DefaultTriggerConfig() TriggerConfig {
	return TriggerConfig{
		Interval: 5 * time.Minute,
	}
}

// TriggerWithConfig runs fn on the configured interval until ctx is cancelled.
func TriggerWithConfig(ctx context.Context, fn func() error, config TriggerConfig) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(config.Interval):
			if err := safeCall(fn); err != nil {
				log.Errorf("self-update check failed: %s", err)
			}
		}
	}
}

func safeCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Panic recovered in self-update: %v", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn()
}

// Trigger runs fn with the default configuration.
func Trigger(ctx context.Context, fn func() error) {
	TriggerWithConfig(ctx, fn, DefaultTriggerConfig())
}
