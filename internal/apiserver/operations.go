package apiserver

import (
	"context"
	"fmt"
	"hostlink/domain/credential"
	"hostlink/domain/metrics"
	"hostlink/domain/task"
	"hostlink/internal/slowrequest"
)

type MetricsOperations interface {
	GetMetricsCreds(ctx context.Context, agentID string) ([]credential.Credential, error)
	PushMetrics(ctx context.Context, payload metrics.MetricPayload) error
}

type HeartbeatResponse struct {
	Message      string      `json:"message"`
	PendingTasks []task.Task `json:"pending_tasks"`
}

// HeartbeatRequest is the heartbeat body. It is sent only when there is
// something to carry; an empty heartbeat posts no body, as it always has.
type HeartbeatRequest struct {
	// SlowRequests are the control-plane requests slower than the threshold
	// since the last delivered heartbeat (see internal/slowrequest).
	SlowRequests []slowrequest.Count `json:"slow_requests,omitempty"`
}

type HeartbeatOperations interface {
	Heartbeat(ctx context.Context, agentID string, slow []slowrequest.Count) (*HeartbeatResponse, error)
}

func (c *client) GetMetricsCreds(ctx context.Context, agentID string) ([]credential.Credential, error) {
	var result []credential.Credential
	err := c.Get(ctx, fmt.Sprintf("/api/v1/agents/%s/credentials", agentID), &result)
	return result, err
}

func (c *client) PushMetrics(ctx context.Context, payload metrics.MetricPayload) error {
	agentID := payload.Resource.AgentID
	return c.Post(ctx, fmt.Sprintf("/api/v1/agents/%s/metrics", agentID), payload, nil)
}

func (c *client) Heartbeat(ctx context.Context, agentID string, slow []slowrequest.Count) (*HeartbeatResponse, error) {
	var body any
	if len(slow) > 0 {
		body = HeartbeatRequest{SlowRequests: slow}
	}

	var result HeartbeatResponse
	err := c.Post(ctx, fmt.Sprintf("/api/v1/agents/%s/heartbeat", agentID), body, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
