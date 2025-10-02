//go:build integration
// +build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"hostlink/app"
	"hostlink/config"
	"hostlink/domain/agent"
	"hostlink/internal/validator"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentTestEnvironment(t *testing.T) (*echo.Echo, *app.Container) {
	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to setup test DB: %v", err)
	}

	container := app.NewContainer(db)
	if err := container.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	e := echo.New()
	e.Validator = validator.New()
	config.AddRoutesV2(e, container)

	return e, container
}

func TestAgentList(t *testing.T) {
	t.Run("lists all agents without filters", func(t *testing.T) {
		e, container := setupAgentTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

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
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent1))
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent2))

		resp, err := http.Get(server.URL + "/api/v1/agents")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		json.NewDecoder(resp.Body).Decode(&agents)
		assert.Len(t, agents, 2)
	})

	t.Run("filters agents by status", func(t *testing.T) {
		e, container := setupAgentTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

		activeAgent := &agent.Agent{
			Fingerprint: "fp-active",
			Hostname:    "active-host",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		inactiveAgent := &agent.Agent{
			Fingerprint: "fp-inactive",
			Hostname:    "inactive-host",
			IPAddress:   "192.168.1.2",
			MACAddress:  "00:11:22:33:44:02",
		}
		require.NoError(t, container.AgentRepository.Create(context.Background(), activeAgent))
		require.NoError(t, container.AgentRepository.Create(context.Background(), inactiveAgent))

		inactiveAgent.Status = "inactive"
		require.NoError(t, container.AgentRepository.Update(context.Background(), inactiveAgent))

		resp, err := http.Get(server.URL + "/api/v1/agents?status=active")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		json.NewDecoder(resp.Body).Decode(&agents)
		assert.Len(t, agents, 1)
		assert.Equal(t, "active", agents[0].Status)
	})

	t.Run("filters agents by fingerprint", func(t *testing.T) {
		e, container := setupAgentTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

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
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent1))
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent2))

		resp, err := http.Get(server.URL + "/api/v1/agents?fingerprint=fp-001")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		json.NewDecoder(resp.Body).Decode(&agents)
		assert.Len(t, agents, 1)
		assert.Equal(t, "fp-001", agents[0].Fingerprint)
	})

	t.Run("combines status and fingerprint filters", func(t *testing.T) {
		e, container := setupAgentTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

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
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent1))
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent2))

		agent2.Status = "inactive"
		require.NoError(t, container.AgentRepository.Update(context.Background(), agent2))

		resp, err := http.Get(server.URL + "/api/v1/agents?status=active&fingerprint=fp-001")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		json.NewDecoder(resp.Body).Decode(&agents)
		assert.Len(t, agents, 1)
		assert.Equal(t, "fp-001", agents[0].Fingerprint)
		assert.Equal(t, "active", agents[0].Status)
	})

	t.Run("returns empty array when no agents match filters", func(t *testing.T) {
		e, container := setupAgentTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

		agent1 := &agent.Agent{
			Fingerprint: "fp-001",
			Hostname:    "host1",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent1))

		resp, err := http.Get(server.URL + "/api/v1/agents?fingerprint=nonexistent")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		json.NewDecoder(resp.Body).Decode(&agents)
		assert.Len(t, agents, 0)
	})

	t.Run("includes tags in response", func(t *testing.T) {
		e, container := setupAgentTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

		a := &agent.Agent{
			Fingerprint: "fp-001",
			Hostname:    "host1",
			IPAddress:   "192.168.1.1",
			MACAddress:  "00:11:22:33:44:01",
		}
		require.NoError(t, container.AgentRepository.Create(context.Background(), a))

		tags := []agent.AgentTag{
			{Key: "env", Value: "prod"},
			{Key: "region", Value: "us-east-1"},
		}
		require.NoError(t, container.AgentRepository.AddTags(context.Background(), a.ID, tags))

		resp, err := http.Get(server.URL + "/api/v1/agents")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		json.NewDecoder(resp.Body).Decode(&agents)
		assert.Len(t, agents, 1)
		assert.Len(t, agents[0].Tags, 2)
	})

	t.Run("returns agents sorted by last seen desc", func(t *testing.T) {
		e, container := setupAgentTestEnvironment(t)
		server := httptest.NewServer(e)
		defer server.Close()

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
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent1))
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent2))
		require.NoError(t, container.AgentRepository.Create(context.Background(), agent3))

		agent1.LastSeen = now.Add(-2 * time.Hour)
		agent2.LastSeen = now
		agent3.LastSeen = now.Add(-1 * time.Hour)
		require.NoError(t, container.AgentRepository.Update(context.Background(), agent1))
		require.NoError(t, container.AgentRepository.Update(context.Background(), agent2))
		require.NoError(t, container.AgentRepository.Update(context.Background(), agent3))

		resp, err := http.Get(server.URL + "/api/v1/agents")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		json.NewDecoder(resp.Body).Decode(&agents)
		assert.Len(t, agents, 3)
		assert.Equal(t, "fp-002", agents[0].Fingerprint)
		assert.Equal(t, "fp-003", agents[1].Fingerprint)
		assert.Equal(t, "fp-001", agents[2].Fingerprint)
	})
}
