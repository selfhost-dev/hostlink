package heartbeatjob

import (
	"context"
	"fmt"
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
			if err := safeCall(fn); err != nil {
				log.Errorf("heartbeat failed: %s", err)
			}
		}
	}
}

func safeCall(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Panic recovered in heartbeat: %v", r)
			err = fmt.Errorf("panic: %v", r)
		}
	}()
	return fn()
}

func Trigger(ctx context.Context, fn func() error) {
	TriggerWithConfig(ctx, fn, DefaultTriggerConfig())
}
