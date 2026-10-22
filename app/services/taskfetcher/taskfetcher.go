// Package taskfetcher fetches the tasks from external URL
package taskfetcher

import (
	"encoding/json"
	"fmt"
	"hostlink/app/services/agentstate"
	"hostlink/app/services/requestsigner"
	"hostlink/config/appconf"
	"hostlink/domain/task"
	"hostlink/internal/httpclient"
	"hostlink/internal/slowrequest"
	"net/http"
	"time"
)

type TaskFetcher interface {
	Fetch() ([]task.Task, error)
}

type taskfetcher struct {
	client          *http.Client
	signer          *requestsigner.RequestSigner
	controlPlaneURL string
	slowReporter    slowrequest.Reporter
}

type Config struct {
	AgentState      *agentstate.AgentState
	PrivateKeyPath  string
	ControlPlaneURL string
	Timeout         time.Duration
	// SlowRequestThreshold is the duration after which a task poll is
	// reported as slow. Zero means the default.
	SlowRequestThreshold time.Duration
}

func New(cfg *Config) (*taskfetcher, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if cfg.SlowRequestThreshold == 0 {
		cfg.SlowRequestThreshold = appconf.APISlowRequestThreshold()
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
			// The shared control-plane transport drops a dead connection, so a
			// stalled task poll does not cost every poll its full timeout.
			Transport: httpclient.SharedControlPlaneTransport(),
		},
		signer:          signer,
		controlPlaneURL: cfg.ControlPlaneURL,
		slowReporter:    slowrequest.New(cfg.SlowRequestThreshold),
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

func (tf *taskfetcher) Fetch() ([]task.Task, error) {
	url := tf.controlPlaneURL + "/api/v1/tasks"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := tf.signer.SignRequest(req); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}
	req.Header.Set(httpclient.CapabilitiesHeader, httpclient.Capabilities)

	started := time.Now()
	tasks, err := tf.fetch(req)
	return tasks, tf.slowReporter.Report(req, time.Since(started), err)
}

// fetch performs the task poll: the HTTP round trip, the status check and the
// decoding of the response.
func (tf *taskfetcher) fetch(req *http.Request) ([]task.Task, error) {
	resp, err := tf.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var tasks []task.Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return tasks, nil
}
