// Package hostlinkcontroller holds the controller details for the hostlink
package hostlinkcontroller

import (
	"context"
	"hostlink/db/schema/taskschema"
	"net/http"

	"github.com/labstack/echo/v4"
)

type Handler struct{}

func NewHandler() *Handler {
	return &Handler{}
}

func (h Handler) Index(c echo.Context) error {
	ctx := context.Background()
	tasks, err := taskschema.All(ctx)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, tasks)
}

func Register(g *echo.Group) {
	h := NewHandler()

	g.GET("/", h.Index)
}
