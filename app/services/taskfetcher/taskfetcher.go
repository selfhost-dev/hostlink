// Package taskfetcher fetches the tasks from external URL
package taskfetcher

import (
	"encoding/json"
	"fmt"
	"hostlink/app/services/agentstate"
	"hostlink/app/services/requestsigner"
	"hostlink/config/appconf"
	"hostlink/db/schema/taskschema"
	"net/http"
	"time"
)

type TaskFetcher interface {
	Fetch() ([]taskschema.Task, error)
}

type taskfetcher struct {
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

func New(cfg *Config) (*taskfetcher, error) {
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

	return &taskfetcher{
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		signer:          signer,
		controlPlaneURL: cfg.ControlPlaneURL,
	}, nil
}

func NewDefault() (*taskfetcher, error) {
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

func (tf *taskfetcher) Fetch() ([]taskschema.Task, error) {
	url := tf.controlPlaneURL + "/api/v1/tasks"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := tf.signer.SignRequest(req); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	resp, err := tf.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var tasks []taskschema.Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return tasks, nil
}
