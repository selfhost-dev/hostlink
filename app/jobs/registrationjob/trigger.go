package registrationjob

import (
	"time"

	"github.com/labstack/gommon/log"
)

// TriggerConfig holds configuration for the Trigger function
type TriggerConfig struct {
	// MaxRetries bounds the number of attempts. Zero or negative means
	// retry forever — an unregistered agent is useless, so giving up
	// permanently bricks the host until a manual restart.
	MaxRetries    int
	InitialDelay  time.Duration
	BackoffFactor int
	// MaxDelay caps the exponential backoff between attempts.
	MaxDelay time.Duration
}

// DefaultTriggerConfig returns the default configuration: retry forever
// with exponential backoff capped at 5 minutes.
func DefaultTriggerConfig() TriggerConfig {
	return TriggerConfig{
		MaxRetries:    0,
		InitialDelay:  10 * time.Second,
		BackoffFactor: 2,
		MaxDelay:      5 * time.Minute,
	}
}

// triggerWithConfig is the internal implementation with configurable delays
func triggerWithConfig(fn func() error, config TriggerConfig) {
	retryDelay := config.InitialDelay

	for attempt := 1; config.MaxRetries <= 0 || attempt <= config.MaxRetries; attempt++ {
		err := fn()
		if err == nil {
			return
		}

		if config.MaxRetries > 0 {
			log.Errorf("Registration attempt %d/%d failed: %v", attempt, config.MaxRetries, err)
			if attempt >= config.MaxRetries {
				break
			}
		} else {
			log.Errorf("Registration attempt %d failed: %v", attempt, err)
		}

		log.Infof("Retrying in %v...", retryDelay)
		time.Sleep(retryDelay)

		retryDelay *= time.Duration(config.BackoffFactor)
		if config.MaxDelay > 0 && retryDelay > config.MaxDelay {
			retryDelay = config.MaxDelay
		}
	}

	log.Error("Agent registration failed after all retry attempts")
}

func TriggerWithConfig(fn func() error, config TriggerConfig) {
	triggerWithConfig(fn, config)
}

func Trigger(fn func() error) {
	triggerWithConfig(fn, DefaultTriggerConfig())
}
