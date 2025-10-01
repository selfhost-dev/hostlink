package agent

import "context"

type Repository interface {
	Create(ctx context.Context, agent *Agent) error
	Update(ctx context.Context, agent *Agent) error
	FindByFingerprint(ctx context.Context, fingerprint string) (*Agent, error)
	FindByID(ctx context.Context, id uint) (*Agent, error)
	GetPublicKeyByAgentID(ctx context.Context, agentID string) (string, error)
	AddTags(ctx context.Context, agentID uint, tags []AgentTag) error
	UpdateTags(ctx context.Context, agentID uint, tags []AgentTag) error
	AddRegistration(ctx context.Context, registration *AgentRegistration) error
	Transaction(ctx context.Context, fn func(Repository) error) error
}