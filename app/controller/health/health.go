// Package health is for the health route
package health

import (
	"hostlink/version"
	"net/http"

	"github.com/labstack/echo/v4"
)

type (
	Handler    struct{}
	OkResponse struct {
		Ok      bool   `json:"ok"`
		Version string `json:"version"`
	}
)

func NewHandler() *Handler {
	return &Handler{}
}

func (h Handler) GET(c echo.Context) error {
	ok := OkResponse{
		Ok:      true,
		Version: version.Version,
	}
	return c.JSON(http.StatusOK, ok)
}

func Register(g *echo.Group) {
	h := NewHandler()

	g.GET("/health", h.GET)
}
