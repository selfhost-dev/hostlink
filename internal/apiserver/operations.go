package apiserver

import (
	"context"
	"fmt"
	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

type Operations interface {
	GetMetricsCreds(ctx context.Context, agentID string) ([]credential.Credential, error)
	PushPostgreSQLMetrics(ctx context.Context, metrics metrics.PostgreSQLMetrics, agentID string) error
}

func (c *client) GetMetricsCreds(ctx context.Context, agentID string) ([]credential.Credential, error) {
	var result []credential.Credential
	err := c.Get(ctx, fmt.Sprintf("/api/v1/agents/%s/credentials", agentID), &result)
	return result, err
}

func (c *client) PushPostgreSQLMetrics(ctx context.Context, req metrics.PostgreSQLMetrics, agentID string) error {
	err := c.Post(ctx, fmt.Sprintf("/api/v1/agents/%s/instances/heartbeat", agentID), req, nil)
	return err
}
