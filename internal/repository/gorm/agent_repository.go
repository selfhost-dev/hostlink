package gorm

import (
	"context"
	"hostlink/domain/agent"
	"time"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

type AgentRepository struct {
	db *gorm.DB
}

func NewAgentRepository(db *gorm.DB) agent.Repository {
	return &AgentRepository{db: db}
}

func (r *AgentRepository) Create(ctx context.Context, a *agent.Agent) error {
	a.ID = "agt_" + ulid.Make().String()
	a.Status = "active"
	a.RegisteredAt = time.Now()
	a.LastSeen = time.Now()
	return r.db.WithContext(ctx).Create(a).Error
}

func (r *AgentRepository) Update(ctx context.Context, a *agent.Agent) error {
	return r.db.WithContext(ctx).Save(a).Error
}

func (r *AgentRepository) FindByFingerprint(ctx context.Context, fingerprint string) (*agent.Agent, error) {
	var a agent.Agent
	err := r.db.WithContext(ctx).Preload("Tags").Where("fingerprint = ?", fingerprint).First(&a).Error
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *AgentRepository) FindByID(ctx context.Context, id string) (*agent.Agent, error) {
	var a agent.Agent
	err := r.db.WithContext(ctx).Preload("Tags").Where("id = ?", id).First(&a).Error
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *AgentRepository) GetPublicKeyByAgentID(ctx context.Context, agentID string) (string, error) {
	var a agent.Agent
	err := r.db.WithContext(ctx).Select("public_key").Where("id = ?", agentID).First(&a).Error
	if err != nil {
		return "", err
	}
	if a.PublicKey == "" {
		return "", agent.ErrPublicKeyNotFound
	}
	return a.PublicKey, nil
}

func (r *AgentRepository) AddTags(ctx context.Context, agentID string, tags []agent.AgentTag) error {
	for i := range tags {
		tags[i].AgentID = agentID
	}
	return r.db.WithContext(ctx).Create(&tags).Error
}

func (r *AgentRepository) UpdateTags(ctx context.Context, agentID string, tags []agent.AgentTag) error {
	// Delete existing tags
	if err := r.db.WithContext(ctx).Where("agent_id = ?", agentID).Delete(&agent.AgentTag{}).Error; err != nil {
		return err
	}
	// Add new tags
	return r.AddTags(ctx, agentID, tags)
}

func (r *AgentRepository) AddRegistration(ctx context.Context, reg *agent.AgentRegistration) error {
	reg.ID = "agr_" + ulid.Make().String()
	return r.db.WithContext(ctx).Create(reg).Error
}

func (r *AgentRepository) FindAll(ctx context.Context, filters agent.AgentFilters) ([]agent.Agent, error) {
	var agents []agent.Agent

	query := r.db.WithContext(ctx).Preload("Tags")

	if filters.Status != nil {
		query = query.Where("status = ?", *filters.Status)
	}

	if filters.Fingerprint != nil {
		query = query.Where("fingerprint = ?", *filters.Fingerprint)
	}

	err := query.Order("last_seen DESC").Find(&agents).Error
	if err != nil {
		return nil, err
	}

	return agents, nil
}

func (r *AgentRepository) Transaction(ctx context.Context, fn func(agent.Repository) error) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		txRepo := &AgentRepository{db: tx}
		return fn(txRepo)
	})
}
