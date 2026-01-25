package update

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	// DefaultStopTimeout is the default timeout for stopping the service.
	DefaultStopTimeout = 30 * time.Second
	// DefaultStartTimeout is the default timeout for starting the service.
	DefaultStartTimeout = 30 * time.Second
)

// ExecFunc is a function type for executing external commands.
// It allows injecting mock implementations for testing.
type ExecFunc func(ctx context.Context, name string, args ...string) ([]byte, error)

// DefaultExecFunc executes commands using os/exec.
func DefaultExecFunc(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// ServiceConfig configures the ServiceController.
type ServiceConfig struct {
	ServiceName  string        // Name of the systemd service (e.g., "hostlink")
	StopTimeout  time.Duration // Timeout for stop operation (default: 30s)
	StartTimeout time.Duration // Timeout for start operation (default: 30s)
	ExecFunc     ExecFunc      // Function to execute commands (for testing)
}

// ServiceController manages systemd service operations.
type ServiceController struct {
	config ServiceConfig
}

// NewServiceController creates a new ServiceController with the given configuration.
func NewServiceController(cfg ServiceConfig) *ServiceController {
	// Apply defaults
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = DefaultStopTimeout
	}
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	if cfg.ExecFunc == nil {
		cfg.ExecFunc = DefaultExecFunc
	}

	return &ServiceController{config: cfg}
}

// Stop stops the systemd service.
// It respects the configured timeout and the parent context.
func (s *ServiceController) Stop(ctx context.Context) error {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, s.config.StopTimeout)
	defer cancel()

	output, err := s.config.ExecFunc(ctx, "systemctl", "stop", s.config.ServiceName)
	if err != nil {
		// Check if context was cancelled/timed out
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("failed to stop service %s: %w (output: %s)", s.config.ServiceName, err, string(output))
	}

	return nil
}

// Exists checks whether the systemd service unit is loaded.
// It runs "systemctl show --property=LoadState <name>" and returns true if
// the LoadState is "loaded".
func (s *ServiceController) Exists(ctx context.Context) (bool, error) {
	output, err := s.config.ExecFunc(ctx, "systemctl", "show", "--property=LoadState", s.config.ServiceName)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, fmt.Errorf("failed to check service %s: %w (output: %s)", s.config.ServiceName, err, string(output))
	}

	// Parse output: "LoadState=loaded\n" or "LoadState=not-found\n"
	line := strings.TrimSpace(string(output))
	return line == "LoadState=loaded", nil
}

// Start starts the systemd service.
// It respects the configured timeout and the parent context.
func (s *ServiceController) Start(ctx context.Context) error {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, s.config.StartTimeout)
	defer cancel()

	output, err := s.config.ExecFunc(ctx, "systemctl", "start", s.config.ServiceName)
	if err != nil {
		// Check if context was cancelled/timed out
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("failed to start service %s: %w (output: %s)", s.config.ServiceName, err, string(output))
	}

	return nil
}
