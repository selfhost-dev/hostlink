package registrationjob

import (
	"time"

	"github.com/labstack/gommon/log"
)

// TriggerConfig holds configuration for the Trigger function
type TriggerConfig struct {
	MaxRetries     int
	InitialDelay   time.Duration
	BackoffFactor  int
}

// DefaultTriggerConfig returns the default configuration
func DefaultTriggerConfig() TriggerConfig {
	return TriggerConfig{
		MaxRetries:     5,
		InitialDelay:   10 * time.Second,
		BackoffFactor:  2,
	}
}

// triggerWithConfig is the internal implementation with configurable delays
func triggerWithConfig(fn func() error, config TriggerConfig) {
	retryDelay := config.InitialDelay

	for attempt := 1; attempt <= config.MaxRetries; attempt++ {
		err := fn()
		if err == nil {
			return
		}

		log.Errorf("Registration attempt %d/%d failed: %v", attempt, config.MaxRetries, err)

		if attempt < config.MaxRetries {
			log.Infof("Retrying in %v...", retryDelay)
			time.Sleep(retryDelay)
			retryDelay *= time.Duration(config.BackoffFactor)
		}
	}

	log.Error("Agent registration failed after all retry attempts")
}

func Trigger(fn func() error) {
	triggerWithConfig(fn, DefaultTriggerConfig())
}