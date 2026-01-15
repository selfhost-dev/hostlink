package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	// ErrHealthCheckFailed is returned when health check fails after all retries.
	ErrHealthCheckFailed = errors.New("health check failed after retries")
	// ErrVersionMismatch is returned when the running version doesn't match expected.
	ErrVersionMismatch = errors.New("version mismatch")
)

const (
	// DefaultHealthRetries is the default number of health check retries.
	DefaultHealthRetries = 5
	// DefaultHealthInterval is the default interval between health checks.
	DefaultHealthInterval = 5 * time.Second
	// DefaultInitialWait is the default wait before first health check.
	DefaultInitialWait = 5 * time.Second
)

// HealthResponse represents the response from the health endpoint.
type HealthResponse struct {
	Ok      bool   `json:"ok"`
	Version string `json:"version"`
}

// HealthConfig configures the HealthChecker.
type HealthConfig struct {
	URL           string              // Health check URL (e.g., http://localhost:8080/health)
	TargetVersion string              // Expected version after update
	MaxRetries    int                 // Max number of retries (default: 5)
	RetryInterval time.Duration       // Time between retries (default: 5s)
	InitialWait   time.Duration       // Initial wait before first check (default: 5s)
	SleepFunc     func(time.Duration) // For testing
	HTTPClient    *http.Client        // Optional custom HTTP client
}

// HealthChecker verifies that the service is healthy after an update.
type HealthChecker struct {
	config HealthConfig
	client *http.Client
}

// NewHealthChecker creates a new HealthChecker with the given configuration.
func NewHealthChecker(cfg HealthConfig) *HealthChecker {
	// Apply defaults
	if cfg.MaxRetries == 0 {
		cfg.MaxRetries = DefaultHealthRetries
	}
	if cfg.RetryInterval == 0 {
		cfg.RetryInterval = DefaultHealthInterval
	}
	if cfg.InitialWait == 0 {
		cfg.InitialWait = DefaultInitialWait
	}
	if cfg.SleepFunc == nil {
		cfg.SleepFunc = time.Sleep
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Second,
		}
	}

	return &HealthChecker{
		config: cfg,
		client: client,
	}
}

// WaitForHealth waits for the service to be healthy with the expected version.
// It performs an initial wait, then retries up to MaxRetries times.
func (h *HealthChecker) WaitForHealth(ctx context.Context) error {
	// Initial wait before first check
	if h.config.InitialWait > 0 {
		h.config.SleepFunc(h.config.InitialWait)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}

	var lastErr error
	// Initial attempt + retries
	totalAttempts := h.config.MaxRetries + 1

	for attempt := 0; attempt < totalAttempts; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		healthy, version, err := h.checkHealth(ctx)
		switch {
		case err != nil:
			lastErr = err
		case !healthy:
			lastErr = errors.New("health check returned ok: false")
		case version != h.config.TargetVersion:
			lastErr = fmt.Errorf("%w: expected %s, got %s", ErrVersionMismatch, h.config.TargetVersion, version)
		default:
			return nil
		}

		if attempt < totalAttempts-1 {
			h.config.SleepFunc(h.config.RetryInterval)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}

	return fmt.Errorf("%w: %v", ErrHealthCheckFailed, lastErr)
}

// checkHealth performs a single health check request.
// Returns (healthy, version, error).
func (h *HealthChecker) checkHealth(ctx context.Context) (bool, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.config.URL, nil)
	if err != nil {
		return false, "", fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		return false, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, "", fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var healthResp HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&healthResp); err != nil {
		return false, "", fmt.Errorf("failed to decode response: %w", err)
	}

	return healthResp.Ok, healthResp.Version, nil
}
