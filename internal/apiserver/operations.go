package apiserver

import (
	"context"
	"fmt"
	"hostlink/domain/credential"
	"hostlink/domain/metrics"
	"hostlink/domain/task"
)

type MetricsOperations interface {
	GetMetricsCreds(ctx context.Context, agentID string) ([]credential.Credential, error)
	PushMetrics(ctx context.Context, payload metrics.MetricPayload) error
}

type HeartbeatResponse struct {
	Message      string      `json:"message"`
	PendingTasks []task.Task `json:"pending_tasks"`
}

type HeartbeatOperations interface {
	Heartbeat(ctx context.Context, agentID string) (*HeartbeatResponse, error)
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

func (c *client) Heartbeat(ctx context.Context, agentID string) (*HeartbeatResponse, error) {
	var result HeartbeatResponse
	err := c.Post(ctx, fmt.Sprintf("/api/v1/agents/%s/heartbeat", agentID), nil, &result)
	if err != nil {
		return nil, err
	}
	return &result, nil
}
