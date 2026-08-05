// Package taskreporter reports task results to the control plane
package taskreporter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hostlink/app/services/agentstate"
	"hostlink/app/services/requestsigner"
	"hostlink/config/appconf"
	"net/http"
	"time"

	"github.com/labstack/gommon/log"
)

type TaskReporter interface {
	Report(taskID string, result *TaskResult) error
}

type TaskResult struct {
	Status   string `json:"status"`
	Output   string `json:"output"`
	Error    string `json:"error"`
	ExitCode int    `json:"exit_code"`
}

type RetryConfig struct {
	MaxRetries        int
	MaxWaitTime       time.Duration
	InitialBackoff    time.Duration
	BackoffMultiplier int
}

type SleepFunc func(time.Duration)

type taskreporter struct {
	client          *http.Client
	signer          *requestsigner.RequestSigner
	controlPlaneURL string
	retryConfig     *RetryConfig
	sleepFunc       SleepFunc
}

type Config struct {
	AgentState      *agentstate.AgentState
	PrivateKeyPath  string
	ControlPlaneURL string
	Timeout         time.Duration
	RetryConfig     *RetryConfig
	SleepFunc       SleepFunc
}

func New(cfg *Config) (*taskreporter, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	retryConfig := cfg.RetryConfig
	if retryConfig == nil {
		// MaxRetries 0 = retry forever. A task result is the only record the
		// control plane gets of the work — dropping it leaves the task stuck
		// in_progress upstream (lease renewals continue) with no recovery path.
		retryConfig = &RetryConfig{
			MaxRetries:        0,
			MaxWaitTime:       5 * time.Minute,
			InitialBackoff:    1000 * time.Millisecond,
			BackoffMultiplier: 2,
		}
	}

	sleepFunc := cfg.SleepFunc
	if sleepFunc == nil {
		sleepFunc = time.Sleep
	}

	agentID := cfg.AgentState.GetAgentID()
	if agentID == "" {
		return nil, fmt.Errorf("agent not registered: missing agent ID")
	}

	signer, err := requestsigner.New(cfg.PrivateKeyPath, agentID)
	if err != nil {
		return nil, fmt.Errorf("failed to create request signer: %w", err)
	}

	return &taskreporter{
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		signer:          signer,
		controlPlaneURL: cfg.ControlPlaneURL,
		retryConfig:     retryConfig,
		sleepFunc:       sleepFunc,
	}, nil
}

func NewDefault() (*taskreporter, error) {
	agentState := agentstate.New(appconf.AgentStatePath())
	if err := agentState.Load(); err != nil {
		return nil, fmt.Errorf("failed to load agent state: %w", err)
	}

	return New(&Config{
		AgentState:      agentState,
		PrivateKeyPath:  appconf.AgentPrivateKeyPath(),
		ControlPlaneURL: appconf.ControlPlaneURL(),
	})
}

func (tr *taskreporter) Report(taskID string, result *TaskResult) error {
	jsonData, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("failed to marshal task result: %w", err)
	}

	url := tr.controlPlaneURL + "/api/v1/tasks/" + taskID

	var lastErr error
	backoff := tr.retryConfig.InitialBackoff

	// MaxRetries <= 0 retries forever: transient outages (tunnel blips, control
	// plane restarts, rate limiting) must never drop a final result. Semantic
	// rejections (non-retryable 4xx) still fail fast — retrying an identical
	// payload the server rejected cannot succeed.
	for attempt := 1; ; attempt++ {
		req, err := http.NewRequest("PUT", url, bytes.NewBuffer(jsonData))
		if err != nil {
			return fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")

		if err := tr.signer.SignRequest(req); err != nil {
			return fmt.Errorf("failed to sign request: %w", err)
		}

		resp, err := tr.client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request failed: %w", err)
		} else {
			statusCode := resp.StatusCode
			resp.Body.Close()

			if statusCode == http.StatusOK {
				return nil
			}

			lastErr = fmt.Errorf("unexpected status code: %d", statusCode)
			retryable := statusCode >= 500 ||
				statusCode == http.StatusTooManyRequests ||
				statusCode == http.StatusRequestTimeout
			if !retryable {
				return lastErr
			}
		}

		if tr.retryConfig.MaxRetries > 0 && attempt > tr.retryConfig.MaxRetries {
			return lastErr
		}

		log.Warnf("failed to report task %s (attempt %d): %v — retrying in %v", taskID, attempt, lastErr, backoff)
		tr.sleepFunc(backoff)
		backoff = min(backoff*time.Duration(tr.retryConfig.BackoffMultiplier), tr.retryConfig.MaxWaitTime)
	}
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
