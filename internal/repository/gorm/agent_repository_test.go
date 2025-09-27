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

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	err = db.AutoMigrate(&agent.Agent{}, &agent.AgentTag{}, &agent.AgentRegistration{})
	require.NoError(t, err)

	return db
}

func TestAgentRepository(t *testing.T) {
	t.Run("Create", func(t *testing.T) {
		db := setupTestDB(t)
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
		db := setupTestDB(t)
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
		db := setupTestDB(t)
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
		db := setupTestDB(t)
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
		db := setupTestDB(t)
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
		db := setupTestDB(t)
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
		db := setupTestDB(t)
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
		db := setupTestDB(t)
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
		db := setupTestDB(t)
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