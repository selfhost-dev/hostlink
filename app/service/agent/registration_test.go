package agent

import (
	"context"
	"errors"
	"hostlink/domain/agent"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockAgentRepository struct {
	createFunc            func(ctx context.Context, agent *agent.Agent) error
	updateFunc            func(ctx context.Context, agent *agent.Agent) error
	findByFingerprintFunc func(ctx context.Context, fp string) (*agent.Agent, error)
	findByIDFunc          func(ctx context.Context, id string) (*agent.Agent, error)
	findAllFunc           func(ctx context.Context, filters agent.AgentFilters) ([]agent.Agent, error)
	getPublicKeyByAgentID func(ctx context.Context, agentID string) (string, error)
	addTagsFunc           func(ctx context.Context, agentID string, tags []agent.AgentTag) error
	updateTagsFunc        func(ctx context.Context, agentID string, tags []agent.AgentTag) error
	addRegistrationFunc   func(ctx context.Context, registration *agent.AgentRegistration) error
	transactionFunc       func(ctx context.Context, fn func(agent.Repository) error) error
}

func (m *mockAgentRepository) Create(ctx context.Context, a *agent.Agent) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, a)
	}
	a.ID = "agt_test123"
	return nil
}

func (m *mockAgentRepository) Update(ctx context.Context, a *agent.Agent) error {
	if m.updateFunc != nil {
		return m.updateFunc(ctx, a)
	}
	return nil
}

func (m *mockAgentRepository) FindByFingerprint(ctx context.Context, fp string) (*agent.Agent, error) {
	if m.findByFingerprintFunc != nil {
		return m.findByFingerprintFunc(ctx, fp)
	}
	return nil, nil
}

func (m *mockAgentRepository) FindByID(ctx context.Context, id string) (*agent.Agent, error) {
	if m.findByIDFunc != nil {
		return m.findByIDFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockAgentRepository) FindAll(ctx context.Context, filters agent.AgentFilters) ([]agent.Agent, error) {
	if m.findAllFunc != nil {
		return m.findAllFunc(ctx, filters)
	}
	return []agent.Agent{}, nil
}

func (m *mockAgentRepository) GetPublicKeyByAgentID(ctx context.Context, agentID string) (string, error) {
	if m.getPublicKeyByAgentID != nil {
		return m.getPublicKeyByAgentID(ctx, agentID)
	}
	return "", nil
}

func (m *mockAgentRepository) AddTags(ctx context.Context, agentID string, tags []agent.AgentTag) error {
	if m.addTagsFunc != nil {
		return m.addTagsFunc(ctx, agentID, tags)
	}
	return nil
}

func (m *mockAgentRepository) UpdateTags(ctx context.Context, agentID string, tags []agent.AgentTag) error {
	if m.updateTagsFunc != nil {
		return m.updateTagsFunc(ctx, agentID, tags)
	}
	return nil
}

func (m *mockAgentRepository) AddRegistration(ctx context.Context, registration *agent.AgentRegistration) error {
	if m.addRegistrationFunc != nil {
		return m.addRegistrationFunc(ctx, registration)
	}
	return nil
}

func (m *mockAgentRepository) Transaction(ctx context.Context, fn func(agent.Repository) error) error {
	if m.transactionFunc != nil {
		return m.transactionFunc(ctx, fn)
	}
	return fn(m)
}

func TestRegistrationService(t *testing.T) {
	t.Run("should return error when token is invalid", func(t *testing.T) {
		mockRepo := &mockAgentRepository{}
		service := NewRegistrationService(mockRepo)
		ctx := context.Background()

		// Empty token ID
		req := RegistrationRequest{
			Fingerprint: "test-fingerprint",
			TokenID:     "",
			TokenKey:    "some-key",
		}

		result, err := service.RegisterAgent(ctx, req)
		assert.Error(t, err)
		assert.Equal(t, ErrInvalidToken, err)
		assert.Nil(t, result)

		// Empty token key
		req.TokenID = "some-id"
		req.TokenKey = ""
		result, err = service.RegisterAgent(ctx, req)
		assert.Error(t, err)
		assert.Equal(t, ErrInvalidToken, err)
		assert.Nil(t, result)

		// Both empty
		req.TokenID = ""
		req.TokenKey = ""
		result, err = service.RegisterAgent(ctx, req)
		assert.Error(t, err)
		assert.Equal(t, ErrInvalidToken, err)
		assert.Nil(t, result)
	})

	t.Run("should successfully register new agent with tags and hardware info", func(t *testing.T) {
		ctx := context.Background()

		var addedTags []agent.AgentTag
		var registrationRecord *agent.AgentRegistration

		mockRepo := &mockAgentRepository{
			findByFingerprintFunc: func(ctx context.Context, fp string) (*agent.Agent, error) {
				return nil, errors.New("not found")
			},
			createFunc: func(ctx context.Context, a *agent.Agent) error {
				a.ID = "agt_new123"
				return nil
			},
			addTagsFunc: func(ctx context.Context, agentID string, tags []agent.AgentTag) error {
				assert.Equal(t, "agt_new123", agentID)
				addedTags = tags
				return nil
			},
			addRegistrationFunc: func(ctx context.Context, reg *agent.AgentRegistration) error {
				registrationRecord = reg
				return nil
			},
		}

		service := NewRegistrationService(mockRepo)

		req := RegistrationRequest{
			Fingerprint:   "new-fingerprint",
			TokenID:       "token-123",
			TokenKey:      "key-456",
			PublicKey:     "ssh-rsa AAAAB3...",
			PublicKeyType: "ssh-rsa",
			Hostname:      "test-host",
			IPAddress:     "192.168.1.100",
			MACAddress:    "00:11:22:33:44:55",
			MachineID:     "machine-123",
			HardwareInfo:  "CPU: Intel i7",
			Tags: []TagPair{
				{Key: "env", Value: "test"},
				{Key: "region", Value: "us-east-1"},
			},
		}

		result, err := service.RegisterAgent(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify agent was created correctly
		assert.Equal(t, "agt_new123", result.ID)
		assert.Equal(t, "new-fingerprint", result.Fingerprint)
		assert.Equal(t, "ssh-rsa AAAAB3...", result.PublicKey)
		assert.Equal(t, "test-host", result.Hostname)
		assert.Equal(t, "192.168.1.100", result.IPAddress)

		// Verify tags were added
		assert.Len(t, addedTags, 2)
		assert.Equal(t, "env", addedTags[0].Key)
		assert.Equal(t, "test", addedTags[0].Value)
		assert.Equal(t, "region", addedTags[1].Key)
		assert.Equal(t, "us-east-1", addedTags[1].Value)

		// Verify registration record
		assert.NotNil(t, registrationRecord)
		assert.Equal(t, "agt_new123", registrationRecord.AgentID)
		assert.Equal(t, "new-fingerprint", registrationRecord.Fingerprint)
		assert.Equal(t, "register", registrationRecord.Event)
		assert.True(t, registrationRecord.Success)
		assert.Equal(t, "CPU: Intel i7", registrationRecord.HardwareSnapshot)
	})

	t.Run("should update existing agent and create re-registration record", func(t *testing.T) {
		ctx := context.Background()

		existingAgent := &agent.Agent{
			ID:          "agt_existing",
			Fingerprint: "existing-fingerprint",
			PublicKey:   "old-key",
			Hostname:    "old-host",
			IPAddress:   "10.0.0.1",
			MACAddress:  "AA:BB:CC:DD:EE:FF",
			LastSeen:    time.Now().Add(-24 * time.Hour),
		}

		var updatedTags []agent.AgentTag
		var registrationRecord *agent.AgentRegistration

		mockRepo := &mockAgentRepository{
			findByFingerprintFunc: func(ctx context.Context, fp string) (*agent.Agent, error) {
				if fp == "existing-fingerprint" {
					return existingAgent, nil
				}
				return nil, errors.New("not found")
			},
			updateFunc: func(ctx context.Context, a *agent.Agent) error {
				return nil
			},
			updateTagsFunc: func(ctx context.Context, agentID string, tags []agent.AgentTag) error {
				assert.Equal(t, "agt_existing", agentID)
				updatedTags = tags
				return nil
			},
			addRegistrationFunc: func(ctx context.Context, reg *agent.AgentRegistration) error {
				registrationRecord = reg
				return nil
			},
		}

		service := NewRegistrationService(mockRepo)

		req := RegistrationRequest{
			Fingerprint:   "existing-fingerprint",
			TokenID:       "new-token-789",
			TokenKey:      "new-key-012",
			PublicKey:     "ssh-rsa NEWKEY...",
			PublicKeyType: "ssh-rsa",
			Hostname:      "new-host",
			IPAddress:     "192.168.2.200",
			MACAddress:    "11:22:33:44:55:66",
			MachineID:     "new-machine-456",
			HardwareInfo:  "CPU: AMD Ryzen",
			Tags: []TagPair{
				{Key: "env", Value: "prod"},
				{Key: "team", Value: "platform"},
			},
		}

		result, err := service.RegisterAgent(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, result)

		// Verify agent was updated
		assert.Equal(t, "agt_existing", result.ID)
		assert.Equal(t, "ssh-rsa NEWKEY...", result.PublicKey)
		assert.Equal(t, "new-host", result.Hostname)
		assert.Equal(t, "192.168.2.200", result.IPAddress)
		assert.Equal(t, "11:22:33:44:55:66", result.MACAddress)
		assert.Equal(t, "new-machine-456", result.MachineID)
		assert.Equal(t, "new-token-789", result.TokenID)

		// Verify tags were updated
		assert.Len(t, updatedTags, 2)
		assert.Equal(t, "env", updatedTags[0].Key)
		assert.Equal(t, "prod", updatedTags[0].Value)
		assert.Equal(t, "team", updatedTags[1].Key)
		assert.Equal(t, "platform", updatedTags[1].Value)

		// Verify re-registration record
		assert.NotNil(t, registrationRecord)
		assert.Equal(t, "agt_existing", registrationRecord.AgentID)
		assert.Equal(t, "existing-fingerprint", registrationRecord.Fingerprint)
		assert.Equal(t, "re-register", registrationRecord.Event)
		assert.True(t, registrationRecord.Success)
		assert.Equal(t, "CPU: AMD Ryzen", registrationRecord.HardwareSnapshot)
	})

	t.Run("should record failed registration attempts in registration table", func(t *testing.T) {
		ctx := context.Background()

		var failedRegistration *agent.AgentRegistration

		mockRepo := &mockAgentRepository{
			findByFingerprintFunc: func(ctx context.Context, fp string) (*agent.Agent, error) {
				return nil, errors.New("not found")
			},
			transactionFunc: func(ctx context.Context, fn func(agent.Repository) error) error {
				return errors.New("database connection failed")
			},
			addRegistrationFunc: func(ctx context.Context, reg *agent.AgentRegistration) error {
				if !reg.Success {
					failedRegistration = reg
				}
				return nil
			},
		}

		service := NewRegistrationService(mockRepo)

		req := RegistrationRequest{
			Fingerprint:   "fail-fingerprint",
			TokenID:       "token-fail",
			TokenKey:      "key-fail",
			PublicKey:     "ssh-rsa FAIL...",
			PublicKeyType: "ssh-rsa",
			Hostname:      "fail-host",
			HardwareInfo:  "Test hardware",
		}

		result, err := service.RegisterAgent(ctx, req)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "database connection failed")
		assert.Nil(t, result)

		// Verify failed registration was recorded
		assert.NotNil(t, failedRegistration)
		assert.Equal(t, "fail-fingerprint", failedRegistration.Fingerprint)
		assert.Equal(t, "register", failedRegistration.Event)
		assert.False(t, failedRegistration.Success)
		assert.Equal(t, "database connection failed", failedRegistration.Error)
		assert.Equal(t, "", failedRegistration.AgentID) // No agent ID since creation failed
	})
}
