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
		retryConfig = &RetryConfig{
			MaxRetries:        5,
			MaxWaitTime:       30 * time.Minute,
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
	var lastErr error
	backoff := tr.retryConfig.InitialBackoff

	for attempt := range tr.retryConfig.MaxRetries + 1 {
		jsonData, err := json.Marshal(result)
		if err != nil {
			return fmt.Errorf("failed to marshal task result: %w", err)
		}

		url := tr.controlPlaneURL + "/api/v1/tasks/" + taskID

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
			if attempt < tr.retryConfig.MaxRetries {
				tr.sleepFunc(backoff)
				backoff = min(backoff*time.Duration(tr.retryConfig.BackoffMultiplier), tr.retryConfig.MaxWaitTime)
			}
			continue
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
			if resp.StatusCode >= 500 && attempt < tr.retryConfig.MaxRetries {
				tr.sleepFunc(backoff)
				backoff = min(backoff*time.Duration(tr.retryConfig.BackoffMultiplier), tr.retryConfig.MaxWaitTime)
				continue
			}
			return lastErr
		}

		return nil
	}

	return lastErr
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
