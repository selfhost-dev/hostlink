package integration

import (
	"context"
	"encoding/json"
	"hostlink/app"
	agentController "hostlink/app/controller/agents"
	"hostlink/domain/agent"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgentDetailsTestEnvironment(t *testing.T) (*echo.Echo, *app.Container) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to setup test DB: %v", err)
	}

	container := app.NewContainer(db)
	if err := container.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	e := echo.New()
	handler := agentController.NewHandlerWithRepo(nil, container.AgentRepository)
	handler.RegisterRoutes(e.Group("/api/v1/agents"))

	return e, container
}

func TestAgentDetails(t *testing.T) {
	t.Run("gets existing agent", func(t *testing.T) {
		e, container := setupAgentDetailsTestEnvironment(t)

		testAgent := &agent.Agent{
			Fingerprint: "test-fp-001",
			Status:      "active",
		}
		err := container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+testAgent.ID, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp agentController.AgentDetailsResponse
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Equal(t, testAgent.ID, resp.ID)
		assert.Equal(t, "test-fp-001", resp.Fingerprint)
	})

	t.Run("returns 404 for non existent agent", func(t *testing.T) {
		e, _ := setupAgentDetailsTestEnvironment(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/nonexistent", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("gets agent with tags", func(t *testing.T) {
		e, container := setupAgentDetailsTestEnvironment(t)

		testAgent := &agent.Agent{
			Fingerprint: "test-fp-with-tags",
			Status:      "active",
			Tags: []agent.AgentTag{
				{Key: "env", Value: "prod"},
				{Key: "region", Value: "us-east"},
			},
		}
		err := container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+testAgent.ID, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp agentController.AgentDetailsResponse
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.Len(t, resp.Tags, 2)
		assert.Equal(t, "env", resp.Tags[0].Key)
		assert.Equal(t, "prod", resp.Tags[0].Value)
	})

	t.Run("returns all required fields", func(t *testing.T) {
		e, container := setupAgentDetailsTestEnvironment(t)

		testAgent := &agent.Agent{
			Fingerprint: "test-fp-fields",
			Status:      "active",
		}
		err := container.AgentRepository.Create(context.Background(), testAgent)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/agents/"+testAgent.ID, nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp agentController.AgentDetailsResponse
		err = json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.NotEmpty(t, resp.ID)
		assert.NotEmpty(t, resp.Fingerprint)
		assert.NotEmpty(t, resp.Status)
		assert.NotZero(t, resp.LastSeen)
		assert.NotZero(t, resp.RegisteredAt)
		assert.NotNil(t, resp.Tags)
	})
}
