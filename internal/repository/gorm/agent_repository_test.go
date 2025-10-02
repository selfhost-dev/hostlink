package gorm

import (
	"context"
	"hostlink/domain/agent"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&agent.Agent{}, &agent.AgentTag{}, &agent.AgentRegistration{})
	require.NoError(t, err)

	return db
}

func TestAgentRepository(t *testing.T) {
	t.Run("Create", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint: "test-fingerprint",
			Hostname:    "test-host",
			IPAddress:   "192.168.1.100",
			MACAddress:  "00:11:22:33:44:55",
		}

		err := repo.Create(ctx, a)
		assert.NoError(t, err)
		assert.NotEmpty(t, a.AID)
		assert.Equal(t, "active", a.Status)
		assert.NotZero(t, a.RegisteredAt)
		assert.NotZero(t, a.LastSeen)
	})

	t.Run("Update", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint: "test-fingerprint",
			Hostname:    "test-host",
			IPAddress:   "192.168.1.100",
			MACAddress:  "00:11:22:33:44:55",
		}
		err := repo.Create(ctx, a)
		require.NoError(t, err)

		a.Hostname = "updated-host"
		a.LastSeen = time.Now().Add(1 * time.Hour)
		err = repo.Update(ctx, a)
		assert.NoError(t, err)

		found, err := repo.FindByID(ctx, a.ID)
		assert.NoError(t, err)
		assert.Equal(t, "updated-host", found.Hostname)
	})

	t.Run("FindByFingerprint", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint: "unique-fingerprint",
			Hostname:    "test-host",
			IPAddress:   "192.168.1.100",
			MACAddress:  "00:11:22:33:44:55",
		}
		err := repo.Create(ctx, a)
		require.NoError(t, err)

		found, err := repo.FindByFingerprint(ctx, "unique-fingerprint")
		assert.NoError(t, err)
		assert.Equal(t, a.ID, found.ID)
		assert.Equal(t, a.Fingerprint, found.Fingerprint)

		notFound, err := repo.FindByFingerprint(ctx, "non-existent")
		assert.Error(t, err)
		assert.Nil(t, notFound)
	})

	t.Run("FindByID", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint: "test-fingerprint",
			Hostname:    "test-host",
			IPAddress:   "192.168.1.100",
			MACAddress:  "00:11:22:33:44:55",
		}
		err := repo.Create(ctx, a)
		require.NoError(t, err)

		found, err := repo.FindByID(ctx, a.ID)
		assert.NoError(t, err)
		assert.Equal(t, a.ID, found.ID)
		assert.Equal(t, a.Fingerprint, found.Fingerprint)

		notFound, err := repo.FindByID(ctx, 99999)
		assert.Error(t, err)
		assert.Nil(t, notFound)
	})

	t.Run("AddTags", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint: "test-fingerprint",
			Hostname:    "test-host",
			IPAddress:   "192.168.1.100",
			MACAddress:  "00:11:22:33:44:55",
		}
		err := repo.Create(ctx, a)
		require.NoError(t, err)

		tags := []agent.AgentTag{
			{Key: "env", Value: "prod"},
			{Key: "region", Value: "us-east-1"},
		}
		err = repo.AddTags(ctx, a.ID, tags)
		assert.NoError(t, err)

		found, err := repo.FindByID(ctx, a.ID)
		assert.NoError(t, err)
		assert.Len(t, found.Tags, 2)
	})

	t.Run("UpdateTags", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint: "test-fingerprint",
			Hostname:    "test-host",
			IPAddress:   "192.168.1.100",
			MACAddress:  "00:11:22:33:44:55",
		}
		err := repo.Create(ctx, a)
		require.NoError(t, err)

		initialTags := []agent.AgentTag{
			{Key: "env", Value: "dev"},
			{Key: "region", Value: "us-west-1"},
		}
		err = repo.AddTags(ctx, a.ID, initialTags)
		require.NoError(t, err)

		newTags := []agent.AgentTag{
			{Key: "env", Value: "prod"},
			{Key: "team", Value: "platform"},
		}
		err = repo.UpdateTags(ctx, a.ID, newTags)
		assert.NoError(t, err)

		found, err := repo.FindByID(ctx, a.ID)
		assert.NoError(t, err)
		assert.Len(t, found.Tags, 2)

		tagMap := make(map[string]string)
		for _, tag := range found.Tags {
			tagMap[tag.Key] = tag.Value
		}
		assert.Equal(t, "prod", tagMap["env"])
		assert.Equal(t, "platform", tagMap["team"])
	})

	t.Run("AddRegistration", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint: "test-fingerprint",
			Hostname:    "test-host",
			IPAddress:   "192.168.1.100",
			MACAddress:  "00:11:22:33:44:55",
		}
		err := repo.Create(ctx, a)
		require.NoError(t, err)

		reg := &agent.AgentRegistration{
			AgentID: a.ID,
			Event:   "registration",
			Success: true,
		}
		err = repo.AddRegistration(ctx, reg)
		assert.NoError(t, err)
		assert.NotZero(t, reg.ID)
		assert.NotEmpty(t, reg.ARID)
	})

	t.Run("Transaction", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		err := repo.Transaction(ctx, func(txRepo agent.Repository) error {
			a := &agent.Agent{
				Fingerprint: "tx-fingerprint",
				Hostname:    "tx-host",
				IPAddress:   "192.168.1.100",
				MACAddress:  "00:11:22:33:44:55",
			}
			err := txRepo.Create(ctx, a)
			if err != nil {
				return err
			}

			tags := []agent.AgentTag{
				{Key: "tx", Value: "test"},
			}
			return txRepo.AddTags(ctx, a.ID, tags)
		})
		assert.NoError(t, err)

		found, err := repo.FindByFingerprint(ctx, "tx-fingerprint")
		assert.NoError(t, err)
		assert.NotNil(t, found)
		assert.Len(t, found.Tags, 1)
		assert.Equal(t, "tx", found.Tags[0].Key)
	})

	t.Run("TransactionRollback", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		err := repo.Transaction(ctx, func(txRepo agent.Repository) error {
			a := &agent.Agent{
				Fingerprint: "rollback-fingerprint",
				Hostname:    "rollback-host",
				IPAddress:   "192.168.1.100",
				MACAddress:  "00:11:22:33:44:55",
			}
			err := txRepo.Create(ctx, a)
			if err != nil {
				return err
			}

			return gorm.ErrInvalidData
		})
		assert.Error(t, err)

		found, err := repo.FindByFingerprint(ctx, "rollback-fingerprint")
		assert.Error(t, err)
		assert.Nil(t, found)
	})
}

func TestGetPublicKeyByAgentID(t *testing.T) {
	t.Run("returns public key when agent exists", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint:   "test-fingerprint",
			Hostname:      "test-host",
			IPAddress:     "192.168.1.100",
			MACAddress:    "00:11:22:33:44:55",
			PublicKey:     "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC...",
			PublicKeyType: "ssh-rsa",
		}
		err := repo.Create(ctx, a)
		require.NoError(t, err)

		publicKey, err := repo.GetPublicKeyByAgentID(ctx, a.AID)
		assert.NoError(t, err)
		assert.Equal(t, "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQC...", publicKey)
	})

	t.Run("returns error when agent not found", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		publicKey, err := repo.GetPublicKeyByAgentID(ctx, "agt_nonexistent")
		assert.Error(t, err)
		assert.Empty(t, publicKey)
	})

	t.Run("returns error when agent has empty public key", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint: "test-fingerprint",
			Hostname:    "test-host",
			IPAddress:   "192.168.1.100",
			MACAddress:  "00:11:22:33:44:55",
			PublicKey:   "",
		}
		err := repo.Create(ctx, a)
		require.NoError(t, err)

		publicKey, err := repo.GetPublicKeyByAgentID(ctx, a.AID)
		assert.ErrorIs(t, err, agent.ErrPublicKeyNotFound)
		assert.Empty(t, publicKey)
	})
}

func TestFindAll(t *testing.T) {
	t.Run("returns all agents without filters", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		agent1 := &agent.Agent{
			Fingerprint: "fp-001",
			Hostname:    "host1",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		agent2 := &agent.Agent{
			Fingerprint: "fp-002",
			Hostname:    "host2",
			IPAddress:   "192.168.1.2",
			MACAddress:  "00:11:22:33:44:02",
		}

		require.NoError(t, repo.Create(ctx, agent1))
		require.NoError(t, repo.Create(ctx, agent2))

		agents, err := repo.FindAll(ctx, agent.AgentFilters{})
		assert.NoError(t, err)
		assert.Len(t, agents, 2)
	})

	t.Run("filters agents by status", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		activeAgent := &agent.Agent{
			Fingerprint: "fp-active",
			Hostname:    "active-host",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		require.NoError(t, repo.Create(ctx, activeAgent))

		inactiveAgent := &agent.Agent{
			Fingerprint: "fp-inactive",
			Hostname:    "inactive-host",
			IPAddress:   "192.168.1.2",
			MACAddress:  "00:11:22:33:44:02",
		}
		require.NoError(t, repo.Create(ctx, inactiveAgent))
		inactiveAgent.Status = "inactive"
		require.NoError(t, repo.Update(ctx, inactiveAgent))

		activeStatus := "active"
		agents, err := repo.FindAll(ctx, agent.AgentFilters{Status: &activeStatus})
		assert.NoError(t, err)
		assert.Len(t, agents, 1)
		assert.Equal(t, "active", agents[0].Status)
	})

	t.Run("filters agents by fingerprint", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		agent1 := &agent.Agent{
			Fingerprint: "fp-001",
			Hostname:    "host1",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		agent2 := &agent.Agent{
			Fingerprint: "fp-002",
			Hostname:    "host2",
			IPAddress:   "192.168.1.2",
			MACAddress:  "00:11:22:33:44:02",
		}

		require.NoError(t, repo.Create(ctx, agent1))
		require.NoError(t, repo.Create(ctx, agent2))

		fingerprint := "fp-001"
		agents, err := repo.FindAll(ctx, agent.AgentFilters{Fingerprint: &fingerprint})
		assert.NoError(t, err)
		assert.Len(t, agents, 1)
		assert.Equal(t, "fp-001", agents[0].Fingerprint)
	})

	t.Run("combines multiple filters", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		agent1 := &agent.Agent{
			Fingerprint: "fp-001",
			Hostname:    "host1",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		agent2 := &agent.Agent{
			Fingerprint: "fp-001",
			Hostname:    "host2",
			IPAddress:   "192.168.1.2",
			MACAddress:  "00:11:22:33:44:02",
		}
		require.NoError(t, repo.Create(ctx, agent1))
		require.NoError(t, repo.Create(ctx, agent2))

		agent2.Status = "inactive"
		require.NoError(t, repo.Update(ctx, agent2))

		activeStatus := "active"
		fingerprint := "fp-001"
		agents, err := repo.FindAll(ctx, agent.AgentFilters{
			Status:      &activeStatus,
			Fingerprint: &fingerprint,
		})
		assert.NoError(t, err)
		assert.Len(t, agents, 1)
		assert.Equal(t, "fp-001", agents[0].Fingerprint)
		assert.Equal(t, "active", agents[0].Status)
	})

	t.Run("returns empty slice when no agents match filters", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		agent1 := &agent.Agent{
			Fingerprint: "fp-001",
			Hostname:    "host1",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		require.NoError(t, repo.Create(ctx, agent1))

		fingerprint := "nonexistent"
		agents, err := repo.FindAll(ctx, agent.AgentFilters{Fingerprint: &fingerprint})
		assert.NoError(t, err)
		assert.Len(t, agents, 0)
	})

	t.Run("preloads tags for each agent", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		a := &agent.Agent{
			Fingerprint: "fp-001",
			Hostname:    "host1",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		require.NoError(t, repo.Create(ctx, a))

		tags := []agent.AgentTag{
			{Key: "env", Value: "prod"},
			{Key: "region", Value: "us-east-1"},
		}
		require.NoError(t, repo.AddTags(ctx, a.ID, tags))

		agents, err := repo.FindAll(ctx, agent.AgentFilters{})
		assert.NoError(t, err)
		assert.Len(t, agents, 1)
		assert.Len(t, agents[0].Tags, 2)
	})

	t.Run("orders agents by last seen desc", func(t *testing.T) {
		db := setupAgentTestDB(t)
		repo := NewAgentRepository(db)
		ctx := context.Background()

		now := time.Now()
		agent1 := &agent.Agent{
			Fingerprint: "fp-001",
			Hostname:    "host1",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		agent2 := &agent.Agent{
			Fingerprint: "fp-002",
			Hostname:    "host2",
			IPAddress:   "192.168.1.2",
			MACAddress:  "00:11:22:33:44:02",
		}
		agent3 := &agent.Agent{
			Fingerprint: "fp-003",
			Hostname:    "host3",
			IPAddress:   "192.168.1.3",
			MACAddress:  "00:11:22:33:44:03",
		}

		require.NoError(t, repo.Create(ctx, agent1))
		require.NoError(t, repo.Create(ctx, agent2))
		require.NoError(t, repo.Create(ctx, agent3))

		agent1.LastSeen = now.Add(-2 * time.Hour)
		agent2.LastSeen = now
		agent3.LastSeen = now.Add(-1 * time.Hour)

		require.NoError(t, repo.Update(ctx, agent1))
		require.NoError(t, repo.Update(ctx, agent2))
		require.NoError(t, repo.Update(ctx, agent3))

		agents, err := repo.FindAll(ctx, agent.AgentFilters{})
		assert.NoError(t, err)
		assert.Len(t, agents, 3)
		assert.Equal(t, "fp-002", agents[0].Fingerprint)
		assert.Equal(t, "fp-003", agents[1].Fingerprint)
		assert.Equal(t, "fp-001", agents[2].Fingerprint)
	})
}
