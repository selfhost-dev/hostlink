// Package agent handles agent registration and management
package agents

import (
	agentService "hostlink/app/service/agent"
	"hostlink/domain/agent"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

type (
	Handler struct {
		registrationSvc agentService.Registrar
		agentRepo       agent.Repository
	}

	// RegistrationRequest represents the incoming registration request from agent
	RegistrationRequest struct {
		Fingerprint   string    `json:"fingerprint" validate:"required"`
		TokenID       string    `json:"token_id" validate:"required"`
		TokenKey      string    `json:"token_key" validate:"required"`
		PublicKey     string    `json:"public_key" validate:"required"`
		PublicKeyType string    `json:"public_key_type" validate:"required"`
		Tags          []TagPair `json:"tags"`
	}

	// TagPair represents a key-value tag
	TagPair struct {
		Key   string `json:"key" validate:"required"`
		Value string `json:"value" validate:"required"`
	}

	// RegistrationResponse returned after successful registration
	RegistrationResponse struct {
		AgentID      string    `json:"agent_id"`
		Fingerprint  string    `json:"fingerprint"`
		Status       string    `json:"status"`
		Message      string    `json:"message"`
		RegisteredAt time.Time `json:"registered_at"`
	}
)

func NewHandler(svc agentService.Registrar) *Handler {
	return &Handler{registrationSvc: svc}
}

func NewHandlerWithRepo(svc agentService.Registrar, repo agent.Repository) *Handler {
	return &Handler{
		registrationSvc: svc,
		agentRepo:       repo,
	}
}

// RegisterAgent handles agent registration at /hostlink/v1/register
func (h *Handler) RegisterAgent(c echo.Context) error {
	var req RegistrationRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Invalid request format: " + err.Error(),
		})
	}

	if err := c.Validate(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Validation failed: " + err.Error(),
		})
	}

	ctx := c.Request().Context()

	// Convert request to service layer format
	svcReq := agentService.RegistrationRequest{
		Fingerprint:   req.Fingerprint,
		TokenID:       req.TokenID,
		TokenKey:      req.TokenKey,
		PublicKey:     req.PublicKey,
		PublicKeyType: req.PublicKeyType,
	}

	// Convert tags
	for _, tag := range req.Tags {
		svcReq.Tags = append(svcReq.Tags, agentService.TagPair{
			Key:   tag.Key,
			Value: tag.Value,
		})
	}

	// Call service
	agent, err := h.registrationSvc.RegisterAgent(ctx, svcReq)
	if err != nil {
		switch err {
		case agentService.ErrInvalidToken:
			return c.JSON(http.StatusUnauthorized, map[string]string{
				"error": "Invalid token",
			})
		case agentService.ErrDuplicateAgent:
			return c.JSON(http.StatusConflict, map[string]string{
				"error": "Agent already exists",
			})
		default:
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": err.Error(),
			})
		}
	}

	// Determine if new or re-registration
	isNewRegistration := agent.CreatedAt.Equal(agent.UpdatedAt)

	// Return success response
	return c.JSON(http.StatusOK, RegistrationResponse{
		AgentID:      agent.AID,
		Fingerprint:  agent.Fingerprint,
		Status:       "registered",
		Message:      determineMessage(isNewRegistration),
		RegisteredAt: agent.RegisteredAt,
	})
}

// List returns all registered agents
func (h *Handler) List(c echo.Context) error {
	ctx := c.Request().Context()

	var filters agent.AgentFilters

	if status := c.QueryParam("status"); status != "" {
		filters.Status = &status
	}

	if fingerprint := c.QueryParam("fingerprint"); fingerprint != "" {
		filters.Fingerprint = &fingerprint
	}

	agents, err := h.agentRepo.FindAll(ctx, filters)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to fetch agents: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, agents)
}

// Show returns details of a specific agent
func (h *Handler) Show(c echo.Context) error {
	// TODO: Implement Show through service layer
	// Steps for implementation:
	// 1. Add GetAgentByAID method to the Registrar interface in app/service/agent/registration.go:
	//    GetAgentByAID(ctx context.Context, aid string) (*agent.Agent, error)
	//
	// 2. Implement GetAgentByAID in RegistrationService:
	//    func (s *RegistrationService) GetAgentByAID(ctx context.Context, aid string) (*agent.Agent, error) {
	//        agent, err := s.agentRepo.FindByAID(ctx, aid)
	//        if err != nil {
	//            // Return domain-specific error for not found
	//            return nil, err
	//        }
	//        return agent, nil
	//    }
	//
	// 3. Add FindByAID method to agent.Repository interface in domain/agent/repository.go:
	//    FindByAID(ctx context.Context, aid string) (*Agent, error)
	//
	// 4. Implement FindByAID in internal/repository/gorm/agent_repository.go:
	//    func (r *AgentRepository) FindByAID(ctx context.Context, aid string) (*agent.Agent, error) {
	//        var a agent.Agent
	//        err := r.db.WithContext(ctx).Preload("Tags").Where("a_id = ?", aid).First(&a).Error
	//        if err != nil {
	//            return nil, err
	//        }
	//        return &a, nil
	//    }
	//
	// 5. Update this controller method to use the service:
	//    aid := c.Param("aid")
	//    agent, err := h.registrationSvc.GetAgentByAID(ctx, aid)
	//    if err == gorm.ErrRecordNotFound {
	//        return c.JSON(http.StatusNotFound, map[string]string{"error": "Agent not found"})
	//    }

	aid := c.Param("aid")
	ctx := c.Request().Context()
	_ = ctx // Remove when implementing
	_ = aid // Remove when implementing

	return c.JSON(http.StatusNotImplemented, map[string]string{
		"error": "Show endpoint not yet implemented",
	})
}

func determineEvent(isNew bool) string {
	if isNew {
		return "register"
	}
	return "re-register"
}

func determineMessage(isNew bool) string {
	if isNew {
		return "Agent successfully registered"
	}
	return "Agent successfully re-registered"
}

// RegisterRoutes registers all agent-related routes
func (h *Handler) RegisterRoutes(g *echo.Group) {
	// Registration endpoint as specified in the issue
	g.POST("/register", h.RegisterAgent)

	// Additional endpoints for management
	g.GET("", h.List)
	g.GET("/:aid", h.Show)
}
