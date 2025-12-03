package heartbeatjob

import (
	"context"
	"time"

	"github.com/labstack/gommon/log"
)

type TriggerConfig struct {
	Interval time.Duration
}

func DefaultTriggerConfig() TriggerConfig {
	return TriggerConfig{
		Interval: 5 * time.Second,
	}
}

func TriggerWithConfig(ctx context.Context, fn func() error, config TriggerConfig) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(config.Interval):
			if err := fn(); err != nil {
				log.Errorf("heartbeat failed: %s", err)
			}
		}
	}
}

func Trigger(ctx context.Context, fn func() error) {
	TriggerWithConfig(ctx, fn, DefaultTriggerConfig())
}
