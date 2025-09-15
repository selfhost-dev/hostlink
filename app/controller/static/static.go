// Package static contains the static file mainly for the UI part
package static

import (
	_ "embed"
	"net/http"

	"github.com/labstack/echo/v4"
)

//go:embed index.html
var indexHTML string

type (
	Handler struct{}
)

func NewHandler() *Handler {
	return &Handler{}
}

func (h Handler) GET(c echo.Context) error {
	return c.HTML(http.StatusOK, indexHTML)
}

func Register(g *echo.Group) {
	h := NewHandler()

	g.GET("/", h.GET)
}
