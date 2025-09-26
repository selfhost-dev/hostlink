package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentService "hostlink/app/service/agent"
	"hostlink/domain/agent"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRegistrationService struct {
	registerAgentFunc func(ctx context.Context, req agentService.RegistrationRequest) (*agent.Agent, error)
}

func (m *mockRegistrationService) RegisterAgent(ctx context.Context, req agentService.RegistrationRequest) (*agent.Agent, error) {
	if m.registerAgentFunc != nil {
		return m.registerAgentFunc(ctx, req)
	}
	return &agent.Agent{}, nil
}

func setupEcho() *echo.Echo {
	e := echo.New()
	e.Validator = &mockValidator{}
	return e
}

type mockValidator struct{}

func (v *mockValidator) Validate(i interface{}) error {
	// Simple validation - check if required fields are present
	if req, ok := i.(*RegistrationRequest); ok {
		if req.Fingerprint == "" || req.TokenID == "" || req.TokenKey == "" ||
			req.PublicKey == "" || req.PublicKeyType == "" {
			return errors.New("validation error: required field missing")
		}
	}
	return nil
}

func TestAgentController(t *testing.T) {
	t.Run("POST /register", func(t *testing.T) {
		t.Run("should successfully register new agent with valid request", func(t *testing.T) {
			mockSvc := &mockRegistrationService{
				registerAgentFunc: func(ctx context.Context, req agentService.RegistrationRequest) (*agent.Agent, error) {
					now := time.Now()
					return &agent.Agent{
						ID:           1,
						AID:          "agt_123456",
						Fingerprint:  req.Fingerprint,
						PublicKey:    req.PublicKey,
						Status:       "active",
						RegisteredAt: now,
						CreatedAt:    now,
						UpdatedAt:    now,
					}, nil
				},
			}

			handler := NewHandler(mockSvc)
			e := setupEcho()

			reqBody := RegistrationRequest{
				Fingerprint:   "test-fingerprint",
				TokenID:       "token-123",
				TokenKey:      "key-456",
				PublicKey:     "ssh-rsa AAAAB3...",
				PublicKeyType: "ssh-rsa",
				Tags: []TagPair{
					{Key: "env", Value: "test"},
				},
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.RegisterAgent(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)

			var resp RegistrationResponse
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "agt_123456", resp.AgentID)
			assert.Equal(t, "test-fingerprint", resp.Fingerprint)
			assert.Equal(t, "registered", resp.Status)
			assert.Equal(t, "Agent successfully registered", resp.Message)
		})

		t.Run("should return 400 when request body has invalid json", func(t *testing.T) {
			mockSvc := &mockRegistrationService{}
			handler := NewHandler(mockSvc)
			e := setupEcho()

			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader([]byte("invalid json")))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.RegisterAgent(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusBadRequest, rec.Code)

			var resp map[string]string
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Contains(t, resp["error"], "Invalid request format")
		})

		t.Run("should return 400 when required fields are missing", func(t *testing.T) {
			mockSvc := &mockRegistrationService{}
			handler := NewHandler(mockSvc)
			e := setupEcho()

			reqBody := RegistrationRequest{
				Fingerprint: "test-fingerprint",
				// Missing TokenID, TokenKey, PublicKey, PublicKeyType
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.RegisterAgent(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusBadRequest, rec.Code)

			var resp map[string]string
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Contains(t, resp["error"], "Validation failed")
		})

		t.Run("should return 401 when token is invalid", func(t *testing.T) {
			mockSvc := &mockRegistrationService{
				registerAgentFunc: func(ctx context.Context, req agentService.RegistrationRequest) (*agent.Agent, error) {
					return nil, agentService.ErrInvalidToken
				},
			}

			handler := NewHandler(mockSvc)
			e := setupEcho()

			reqBody := RegistrationRequest{
				Fingerprint:   "test-fingerprint",
				TokenID:       "invalid-token",
				TokenKey:      "invalid-key",
				PublicKey:     "ssh-rsa AAAAB3...",
				PublicKeyType: "ssh-rsa",
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.RegisterAgent(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusUnauthorized, rec.Code)

			var resp map[string]string
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "Invalid token", resp["error"])
		})

		t.Run("should return 409 when agent already exists", func(t *testing.T) {
			mockSvc := &mockRegistrationService{
				registerAgentFunc: func(ctx context.Context, req agentService.RegistrationRequest) (*agent.Agent, error) {
					return nil, agentService.ErrDuplicateAgent
				},
			}

			handler := NewHandler(mockSvc)
			e := setupEcho()

			reqBody := RegistrationRequest{
				Fingerprint:   "existing-fingerprint",
				TokenID:       "token-123",
				TokenKey:      "key-456",
				PublicKey:     "ssh-rsa AAAAB3...",
				PublicKeyType: "ssh-rsa",
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.RegisterAgent(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusConflict, rec.Code)

			var resp map[string]string
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "Agent already exists", resp["error"])
		})

		t.Run("should return 500 when service returns unexpected error", func(t *testing.T) {
			mockSvc := &mockRegistrationService{
				registerAgentFunc: func(ctx context.Context, req agentService.RegistrationRequest) (*agent.Agent, error) {
					return nil, errors.New("database connection failed")
				},
			}

			handler := NewHandler(mockSvc)
			e := setupEcho()

			reqBody := RegistrationRequest{
				Fingerprint:   "test-fingerprint",
				TokenID:       "token-123",
				TokenKey:      "key-456",
				PublicKey:     "ssh-rsa AAAAB3...",
				PublicKeyType: "ssh-rsa",
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.RegisterAgent(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusInternalServerError, rec.Code)

			var resp map[string]string
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "database connection failed", resp["error"])
		})

		t.Run("should return correct message for new registration", func(t *testing.T) {
			mockSvc := &mockRegistrationService{
				registerAgentFunc: func(ctx context.Context, req agentService.RegistrationRequest) (*agent.Agent, error) {
					now := time.Now()
					return &agent.Agent{
						ID:           1,
						AID:          "agt_new123",
						Fingerprint:  req.Fingerprint,
						Status:       "active",
						RegisteredAt: now,
						CreatedAt:    now,
						UpdatedAt:    now, // Same as CreatedAt for new registration
					}, nil
				},
			}

			handler := NewHandler(mockSvc)
			e := setupEcho()

			reqBody := RegistrationRequest{
				Fingerprint:   "new-fingerprint",
				TokenID:       "token-123",
				TokenKey:      "key-456",
				PublicKey:     "ssh-rsa AAAAB3...",
				PublicKeyType: "ssh-rsa",
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.RegisterAgent(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)

			var resp RegistrationResponse
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "Agent successfully registered", resp.Message)
		})

		t.Run("should return correct message for re-registration", func(t *testing.T) {
			mockSvc := &mockRegistrationService{
				registerAgentFunc: func(ctx context.Context, req agentService.RegistrationRequest) (*agent.Agent, error) {
					now := time.Now()
					return &agent.Agent{
						ID:           1,
						AID:          "agt_existing123",
						Fingerprint:  req.Fingerprint,
						Status:       "active",
						RegisteredAt: now.Add(-24 * time.Hour),
						CreatedAt:    now.Add(-48 * time.Hour), // Created earlier
						UpdatedAt:    now,                       // Updated now (different from CreatedAt)
					}, nil
				},
			}

			handler := NewHandler(mockSvc)
			e := setupEcho()

			reqBody := RegistrationRequest{
				Fingerprint:   "existing-fingerprint",
				TokenID:       "token-123",
				TokenKey:      "key-456",
				PublicKey:     "ssh-rsa UPDATED...",
				PublicKeyType: "ssh-rsa",
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.RegisterAgent(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)

			var resp RegistrationResponse
			err = json.Unmarshal(rec.Body.Bytes(), &resp)
			require.NoError(t, err)
			assert.Equal(t, "Agent successfully re-registered", resp.Message)
		})

		t.Run("should properly convert tags from request to service format", func(t *testing.T) {
			var capturedRequest agentService.RegistrationRequest

			mockSvc := &mockRegistrationService{
				registerAgentFunc: func(ctx context.Context, req agentService.RegistrationRequest) (*agent.Agent, error) {
					capturedRequest = req
					now := time.Now()
					return &agent.Agent{
						ID:           1,
						AID:          "agt_123456",
						Fingerprint:  req.Fingerprint,
						Status:       "active",
						RegisteredAt: now,
						CreatedAt:    now,
						UpdatedAt:    now,
					}, nil
				},
			}

			handler := NewHandler(mockSvc)
			e := setupEcho()

			reqBody := RegistrationRequest{
				Fingerprint:   "test-fingerprint",
				TokenID:       "token-123",
				TokenKey:      "key-456",
				PublicKey:     "ssh-rsa AAAAB3...",
				PublicKeyType: "ssh-rsa",
				Tags: []TagPair{
					{Key: "env", Value: "production"},
					{Key: "region", Value: "us-east-1"},
					{Key: "team", Value: "platform"},
				},
			}

			body, _ := json.Marshal(reqBody)
			req := httptest.NewRequest(http.MethodPost, "/register", bytes.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := handler.RegisterAgent(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code)

			// Verify tags were properly converted
			assert.Len(t, capturedRequest.Tags, 3)
			assert.Equal(t, "env", capturedRequest.Tags[0].Key)
			assert.Equal(t, "production", capturedRequest.Tags[0].Value)
			assert.Equal(t, "region", capturedRequest.Tags[1].Key)
			assert.Equal(t, "us-east-1", capturedRequest.Tags[1].Value)
			assert.Equal(t, "team", capturedRequest.Tags[2].Key)
			assert.Equal(t, "platform", capturedRequest.Tags[2].Value)
		})
	})

	t.Run("GET /agents", func(t *testing.T) {
		t.Run("should return all agents when successful", func(t *testing.T) {
			t.Skip("TODO: Implement when List is moved to service layer")
		})

		t.Run("should return empty array when no agents exist", func(t *testing.T) {
			t.Skip("TODO: Implement when List is moved to service layer")
		})

		t.Run("should return 500 when database error occurs", func(t *testing.T) {
			t.Skip("TODO: Implement when List is moved to service layer")
		})
	})

	t.Run("GET /agents/:aid", func(t *testing.T) {
		t.Run("should return agent details when agent exists", func(t *testing.T) {
			t.Skip("TODO: Implement when Show is moved to service layer")
		})

		t.Run("should return 404 when agent not found", func(t *testing.T) {
			t.Skip("TODO: Implement when Show is moved to service layer")
		})

		t.Run("should return 500 when database error occurs", func(t *testing.T) {
			t.Skip("TODO: Implement when Show is moved to service layer")
		})

		t.Run("should extract agent id from url parameter correctly", func(t *testing.T) {
			t.Skip("TODO: Implement when Show is moved to service layer")
		})
	})
}