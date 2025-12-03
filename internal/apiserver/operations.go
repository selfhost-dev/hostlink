package apiserver

import (
	"context"
	"fmt"
	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

type Operations interface {
	GetMetricsCreds(ctx context.Context, agentID string) ([]credential.Credential, error)
	PushMetrics(ctx context.Context, payload metrics.MetricPayload) error
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

// TODO: Add heartbeat endpoint with higher ping frequency
