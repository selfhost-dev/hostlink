package apiserver

import (
	"fmt"
	"time"

	"hostlink/config/appconf"
)

type Config struct {
	baseURL        string
	privateKeyPath string
	agentStatePath string
	timeout        time.Duration
	maxRetries     int
	// slowRequestThreshold is the duration after which a control-plane request
	// is reported as slow.
	slowRequestThreshold time.Duration
}

func (c *Config) Validate() error {
	if c.baseURL == "" {
		return fmt.Errorf("base URL is required")
	}
	if c.agentStatePath == "" {
		return fmt.Errorf("agent ID is required")
	}
	if c.privateKeyPath == "" {
		return fmt.Errorf("private key path is required")
	}
	if c.timeout == 0 {
		c.timeout = 30 * time.Second
	}
	if c.slowRequestThreshold == 0 {
		c.slowRequestThreshold = appconf.DefaultAPISlowRequestThreshold
	}
	return nil
}
