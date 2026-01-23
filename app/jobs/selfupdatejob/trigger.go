package selfupdatejob

import (
	"context"
	"time"

	log "github.com/sirupsen/logrus"
)

// TriggerConfig holds configuration for the trigger.
type TriggerConfig struct {
	Interval time.Duration
}

// DefaultTriggerConfig returns the default trigger configuration (1 hour interval).
func DefaultTriggerConfig() TriggerConfig {
	return TriggerConfig{
		Interval: 1 * time.Hour,
	}
}

// TriggerWithConfig runs fn on the configured interval until ctx is cancelled.
func TriggerWithConfig(ctx context.Context, fn func() error, config TriggerConfig) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(config.Interval):
			if err := fn(); err != nil {
				log.Errorf("self-update check failed: %s", err)
			}
		}
	}
}

// Trigger runs fn with the default configuration.
func Trigger(ctx context.Context, fn func() error) {
	TriggerWithConfig(ctx, fn, DefaultTriggerConfig())
}
