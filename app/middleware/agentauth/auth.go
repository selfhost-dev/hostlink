package agentauth

import (
	"context"
	"net/http"

	"hostlink/app/services/reqauth"
	"hostlink/internal/crypto"

	"github.com/labstack/echo/v4"
)

type AgentRepository interface {
	GetPublicKeyByAgentID(ctx context.Context, agentID string) (string, error)
}

func Middleware(repo AgentRepository) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			agentID := c.Request().Header.Get("X-Agent-ID")
			if agentID == "" {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing agent ID")
			}

			publicKeyBase64, err := repo.GetPublicKeyByAgentID(c.Request().Context(), agentID)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "authentication failed")
			}

			publicKey, err := crypto.ParsePublicKeyFromBase64(publicKeyBase64)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid public key")
			}

			authenticator := reqauth.New(publicKey)
			if err := authenticator.Authenticate(c.Request()); err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "authentication failed")
			}

			return next(c)
		}
	}
}
