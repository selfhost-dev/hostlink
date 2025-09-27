package agent

import (
	"context"
	"errors"
	"hostlink/domain/agent"
	"time"
)

var (
	ErrInvalidToken   = errors.New("invalid token")
	ErrDuplicateAgent = errors.New("agent already exists")
)

// Registrar defines the interface for agent registration operations
type Registrar interface {
	RegisterAgent(ctx context.Context, req RegistrationRequest) (*agent.Agent, error)
}

type RegistrationRequest struct {
	Fingerprint   string    `json:"fingerprint"`
	TokenID       string    `json:"token_id"`
	TokenKey      string    `json:"token_key"`
	PublicKey     string    `json:"public_key"`
	PublicKeyType string    `json:"public_key_type"`
	Tags          []TagPair `json:"tags"`

	// Hardware info
	Hostname     string `json:"hostname,omitempty"`
	IPAddress    string `json:"ip_address,omitempty"`
	MACAddress   string `json:"mac_address,omitempty"`
	MachineID    string `json:"machine_id,omitempty"`
	HardwareInfo string `json:"hardware_info,omitempty"`
}

type TagPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type RegistrationService struct {
	agentRepo agent.Repository
}

func NewRegistrationService(repo agent.Repository) *RegistrationService {
	return &RegistrationService{agentRepo: repo}
}

func (s *RegistrationService) RegisterAgent(ctx context.Context, req RegistrationRequest) (*agent.Agent, error) {
	// TODO: Implement proper token validation
	if !s.isValidToken(req.TokenID, req.TokenKey) {
		return nil, ErrInvalidToken
	}

	// Check for existing agent
	existing, err := s.agentRepo.FindByFingerprint(ctx, req.Fingerprint)
	if err == nil && existing != nil {
		return s.handleReregistration(ctx, existing, req)
	}

	// Create new agent with transaction
	var newAgent *agent.Agent
	err = s.agentRepo.Transaction(ctx, func(txRepo agent.Repository) error {
		// Create agent
		newAgent = &agent.Agent{
			Fingerprint:   req.Fingerprint,
			PublicKey:     req.PublicKey,
			PublicKeyType: req.PublicKeyType,
			TokenID:       req.TokenID,
			Hostname:      req.Hostname,
			IPAddress:     req.IPAddress,
			MACAddress:    req.MACAddress,
			MachineID:     req.MachineID,
		}

		if err := txRepo.Create(ctx, newAgent); err != nil {
			return err
		}

		// Add tags
		if len(req.Tags) > 0 {
			var tags []agent.AgentTag
			for _, tag := range req.Tags {
				tags = append(tags, agent.AgentTag{
					Key:   tag.Key,
					Value: tag.Value,
				})
			}
			if err := txRepo.AddTags(ctx, newAgent.ID, tags); err != nil {
				return err
			}
		}

		// Add registration record
		registration := &agent.AgentRegistration{
			AgentID:          newAgent.ID,
			Fingerprint:      req.Fingerprint,
			Event:            "register",
			Success:          true,
			HardwareSnapshot: req.HardwareInfo,
		}

		return txRepo.AddRegistration(ctx, registration)
	})

	if err != nil {
		// Log failed registration attempt
		failedReg := &agent.AgentRegistration{
			Fingerprint: req.Fingerprint,
			Event:       "register",
			Success:     false,
			Error:       err.Error(),
		}
		_ = s.agentRepo.AddRegistration(ctx, failedReg)
		return nil, err
	}

	return newAgent, nil
}

func (s *RegistrationService) handleReregistration(ctx context.Context, existing *agent.Agent, req RegistrationRequest) (*agent.Agent, error) {
	// Update existing agent
	existing.PublicKey = req.PublicKey
	existing.PublicKeyType = req.PublicKeyType
	existing.TokenID = req.TokenID
	existing.LastSeen = time.Now()
	existing.Hostname = req.Hostname
	existing.IPAddress = req.IPAddress
	existing.MACAddress = req.MACAddress
	existing.MachineID = req.MachineID

	err := s.agentRepo.Transaction(ctx, func(txRepo agent.Repository) error {
		if err := txRepo.Update(ctx, existing); err != nil {
			return err
		}

		// Update tags
		if len(req.Tags) > 0 {
			var tags []agent.AgentTag
			for _, tag := range req.Tags {
				tags = append(tags, agent.AgentTag{
					Key:   tag.Key,
					Value: tag.Value,
				})
			}
			if err := txRepo.UpdateTags(ctx, existing.ID, tags); err != nil {
				return err
			}
		}

		// Add re-registration record
		registration := &agent.AgentRegistration{
			AgentID:          existing.ID,
			Fingerprint:      req.Fingerprint,
			Event:            "re-register",
			Success:          true,
			HardwareSnapshot: req.HardwareInfo,
		}

		return txRepo.AddRegistration(ctx, registration)
	})

	return existing, err
}

func (s *RegistrationService) isValidToken(tokenID, tokenKey string) bool {
	// TODO: Implement actual token validation against configuration
	// For now, accept any non-empty values
	return tokenID != "" && tokenKey != ""
}