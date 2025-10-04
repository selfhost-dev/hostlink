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

type taskreporter struct {
	client          *http.Client
	signer          *requestsigner.RequestSigner
	controlPlaneURL string
}

type Config struct {
	AgentState      *agentstate.AgentState
	PrivateKeyPath  string
	ControlPlaneURL string
	Timeout         time.Duration
}

func New(cfg *Config) (*taskreporter, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
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
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return nil
}
