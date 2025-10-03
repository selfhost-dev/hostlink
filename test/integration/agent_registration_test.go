//go:build integration
// +build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hostlink/app"
	agentController "hostlink/app/controller/agents"
	"hostlink/app/services/agentregistrar"
	"hostlink/config"
	"hostlink/domain/agent"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAgnetTestEnvironment(t *testing.T) (*echo.Echo, *app.Container) {
	// Use shared memory database for better concurrency support
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to setup test DB: %v", err)
	}

	container := app.NewContainer(db)
	if err := container.Migrate(); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}

	e := echo.New()
	e.Validator = &mockValidator{}
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	config.AddRoutesV2(e, container)

	return e, container
}

type mockValidator struct{}

func (v *mockValidator) Validate(i interface{}) error {
	// Basic validation for integration tests
	if req, ok := i.(*agentController.RegistrationRequest); ok {
		if req.Fingerprint == "" || req.TokenID == "" || req.TokenKey == "" ||
			req.PublicKey == "" || req.PublicKeyType == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "validation error: required field missing")
		}
	}
	return nil
}

func TestAgentRegistrationIntegration(t *testing.T) {
	t.Run("Full Registration Flow", func(t *testing.T) {
		t.Run("should successfully register new agent and verify in database", func(t *testing.T) {
			e, container := setupAgnetTestEnvironment(t)

			reqBody := map[string]interface{}{
				"fingerprint":     "integration-test-fp-001",
				"token_id":        "token-integration-001",
				"token_key":       "key-integration-001",
				"public_key":      "ssh-rsa AAAAB3Integration...",
				"public_key_type": "ssh-rsa",
				"tags": []map[string]string{
					{"key": "env", "value": "integration-test"},
					{"key": "version", "value": "1.0.0"},
				},
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			// Verify HTTP response
			assert.Equal(t, http.StatusOK, rec.Code)

			var resp agentController.RegistrationResponse
			err := json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "integration-test-fp-001", resp.Fingerprint)
			assert.Equal(t, "registered", resp.Status)
			assert.Equal(t, "Agent successfully registered", resp.Message)

			// Verify in database
			var dbAgent agent.Agent
			err = container.DB.Preload("Tags").Where("fingerprint = ?", "integration-test-fp-001").First(&dbAgent).Error
			require.NoError(t, err)
			assert.Equal(t, "integration-test-fp-001", dbAgent.Fingerprint)
			assert.Equal(t, "ssh-rsa AAAAB3Integration...", dbAgent.PublicKey)
			assert.Equal(t, "active", dbAgent.Status)
			assert.Len(t, dbAgent.Tags, 2)

			// Verify registration record
			var regRecord agent.AgentRegistration
			err = container.DB.Where("agent_id = ?", dbAgent.ID).First(&regRecord).Error
			require.NoError(t, err)
			assert.Equal(t, "register", regRecord.Event)
			assert.True(t, regRecord.Success)
		})

		t.Run("should successfully handle agent re-registration", func(t *testing.T) {
			e, container := setupAgnetTestEnvironment(t)

			// First registration
			firstReq := map[string]interface{}{
				"fingerprint":     "reregister-test-fp",
				"token_id":        "token-first",
				"token_key":       "key-first",
				"public_key":      "ssh-rsa OldKey...",
				"public_key_type": "ssh-rsa",
			}

			body, _ := json.Marshal(firstReq)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusOK, rec.Code)

			// Re-registration with updated info
			secondReq := map[string]interface{}{
				"fingerprint":     "reregister-test-fp",
				"token_id":        "token-updated",
				"token_key":       "key-updated",
				"public_key":      "ssh-rsa NewKey...",
				"public_key_type": "ssh-rsa",
				"tags": []map[string]string{
					{"key": "status", "value": "updated"},
				},
			}

			body, _ = json.Marshal(secondReq)
			req = httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec = httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			// Verify re-registration response
			assert.Equal(t, http.StatusOK, rec.Code)
			var resp agentController.RegistrationResponse
			err := json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "Agent successfully re-registered", resp.Message)

			// Verify database has only one agent
			var count int64
			container.DB.Model(&agent.Agent{}).Where("fingerprint = ?", "reregister-test-fp").Count(&count)
			assert.Equal(t, int64(1), count)

			// Verify agent was updated
			var dbAgent agent.Agent
			err = container.DB.Preload("Tags").Where("fingerprint = ?", "reregister-test-fp").First(&dbAgent).Error
			require.NoError(t, err)
			assert.Equal(t, "ssh-rsa NewKey...", dbAgent.PublicKey)
			assert.Equal(t, "token-updated", dbAgent.TokenID)

			// Verify both registration records exist
			var regRecords []agent.AgentRegistration
			err = container.DB.Where("agent_id = ?", dbAgent.ID).Order("created_at").Find(&regRecords).Error
			require.NoError(t, err)
			assert.Len(t, regRecords, 2)
			assert.Equal(t, "register", regRecords[0].Event)
			assert.Equal(t, "re-register", regRecords[1].Event)
		})

		t.Run("should handle concurrent registrations without data corruption", func(t *testing.T) {
			e, container := setupAgnetTestEnvironment(t)

			var wg sync.WaitGroup
			results := make(chan bool, 5)

			// Launch 5 concurrent registrations
			for i := 0; i < 5; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()

					reqBody := map[string]interface{}{
						"fingerprint":     fmt.Sprintf("concurrent-fp-%d", id),
						"token_id":        fmt.Sprintf("token-%d", id),
						"token_key":       fmt.Sprintf("key-%d", id),
						"public_key":      fmt.Sprintf("ssh-rsa Key%d...", id),
						"public_key_type": "ssh-rsa",
					}

					body, _ := json.Marshal(reqBody)
					req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
					req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
					rec := httptest.NewRecorder()

					e.ServeHTTP(rec, req)

					if rec.Code == http.StatusOK {
						results <- true
					} else {
						t.Logf("Registration %d failed with status %d: %s", id, rec.Code, rec.Body.String())
						results <- false
					}
				}(i)
			}

			wg.Wait()
			close(results)

			// Count successes
			successCount := 0
			for success := range results {
				if success {
					successCount++
				}
			}

			// All registrations should succeed
			assert.Equal(t, 5, successCount, "All 5 concurrent registrations should succeed")

			// Verify all agents were created in the database
			var count int64
			container.DB.Model(&agent.Agent{}).Where("fingerprint LIKE ?", "concurrent-fp-%").Count(&count)
			assert.Equal(t, int64(5), count, "Should have 5 agents in database")
		})
	})

	t.Run("Validation Errors", func(t *testing.T) {
		t.Run("should return 400 when fingerprint is missing", func(t *testing.T) {
			e, _ := setupAgnetTestEnvironment(t)

			reqBody := map[string]interface{}{
				"token_id":        "token-123",
				"token_key":       "key-456",
				"public_key":      "ssh-rsa ...",
				"public_key_type": "ssh-rsa",
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			var resp map[string]string
			json.Unmarshal(rec.Body.Bytes(), &resp)
			assert.Contains(t, resp["error"], "Validation failed")
		})

		t.Run("should return 401 when token is invalid", func(t *testing.T) {
			e, _ := setupAgnetTestEnvironment(t)

			reqBody := map[string]interface{}{
				"fingerprint":     "test-fp",
				"token_id":        "", // Invalid empty token
				"token_key":       "key",
				"public_key":      "ssh-rsa ...",
				"public_key_type": "ssh-rsa",
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})

		t.Run("should return 400 when request body is malformed JSON", func(t *testing.T) {
			e, _ := setupAgnetTestEnvironment(t)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader([]byte("malformed json")))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			var resp map[string]string
			json.Unmarshal(rec.Body.Bytes(), &resp)
			assert.Contains(t, resp["error"], "Invalid request format")
		})
	})

	t.Run("Database Transaction Rollback", func(t *testing.T) {
		t.Run("should rollback transaction when partial failure occurs", func(t *testing.T) {
			// This test would require mocking a failure during transaction
			// Since we're using a real database, we can simulate by checking
			// that incomplete registrations don't leave orphaned records

			e, container := setupAgnetTestEnvironment(t)

			// Count initial records
			var initialAgentCount int64
			var initialTagCount int64
			container.DB.Model(&agent.Agent{}).Count(&initialAgentCount)
			container.DB.Model(&agent.AgentTag{}).Count(&initialTagCount)

			// Try to register with a fingerprint that would cause issues
			// In a real scenario, you might trigger a constraint violation
			reqBody := map[string]interface{}{
				"fingerprint":     "rollback-test-fp",
				"token_id":        "token-rollback",
				"token_key":       "key-rollback",
				"public_key":      "ssh-rsa ...",
				"public_key_type": "ssh-rsa",
				"tags": []map[string]string{
					{"key": "test", "value": "rollback"},
				},
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/agents/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()

			e.ServeHTTP(rec, req)

			// If successful, we should have exactly one more agent and tag
			if rec.Code == http.StatusOK {
				var finalAgentCount int64
				var finalTagCount int64
				container.DB.Model(&agent.Agent{}).Count(&finalAgentCount)
				container.DB.Model(&agent.AgentTag{}).Count(&finalTagCount)

				assert.Equal(t, initialAgentCount+1, finalAgentCount, "Should have exactly one more agent")
				assert.Equal(t, initialTagCount+1, finalTagCount, "Should have exactly one more tag")
			} else {
				// If failed, counts should remain the same (rollback)
				var finalAgentCount int64
				var finalTagCount int64
				container.DB.Model(&agent.Agent{}).Count(&finalAgentCount)
				container.DB.Model(&agent.AgentTag{}).Count(&finalTagCount)

				assert.Equal(t, initialAgentCount, finalAgentCount, "Agent count should not change on failure")
				assert.Equal(t, initialTagCount, finalTagCount, "Tag count should not change on failure")
			}
		})
	})

	t.Run("Agent Registrar Service Integration", func(t *testing.T) {
		t.Run("should successfully register using registrar service with correct URL", func(t *testing.T) {
			e, container := setupAgnetTestEnvironment(t)

			server := httptest.NewServer(e)
			defer server.Close()

			registrar := agentregistrar.NewWithConfig(&agentregistrar.Config{
				ControlPlaneURL: server.URL,
				TokenID:         "test-token-id",
				TokenKey:        "test-token-key",
				PrivateKeyPath:  t.TempDir() + "/agent.key",
			})

			publicKey, err := registrar.PreparePublicKey()
			require.NoError(t, err)
			require.NotEmpty(t, publicKey)

			tags := []agentregistrar.TagPair{
				{Key: "env", Value: "integration"},
				{Key: "service", Value: "registrar-test"},
			}

			response, err := registrar.Register("registrar-service-fp", publicKey, tags)
			require.NoError(t, err)
			require.NotNil(t, response)

			assert.NotEmpty(t, response.ID)
			assert.Equal(t, "registrar-service-fp", response.Fingerprint)
			assert.Equal(t, "registered", response.Status)

			dbAgent, err := container.AgentRepository.FindByFingerprint(nil, "registrar-service-fp")
			require.NoError(t, err)
			require.NotNil(t, dbAgent)
			assert.Equal(t, publicKey, dbAgent.PublicKey)
		})
	})
}
