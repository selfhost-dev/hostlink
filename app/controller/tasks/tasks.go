// Package tasks handles all the command requests
package tasks

import (
	"database/sql"
	"hostlink/db/schema/taskschema"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mattn/go-shellwords"
)

type (
	Handler   struct{}
	OkCommand struct {
		Command string `json:"command"`
	}
	TaskRequest struct {
		Command  string `json:"command"`
		Priority int    `json:"priority"`
	}
)

func NewHandler() *Handler {
	return &Handler{}
}

func (h Handler) Create(c echo.Context) error {
	var task taskschema.Task
	if err := c.Bind(&task); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}

	_, err := shellwords.Parse(task.Command)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Invalid command syntax: " + err.Error(),
		})
	}

	ctx := c.Request().Context()

	err = task.Save(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to save command: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, task)
}

func (h Handler) Index(c echo.Context) error {
	ctx := c.Request().Context()

	tasks, err := taskschema.All(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to fetch tasks: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, tasks)
}

func (h Handler) Show(c echo.Context) error {
	ctx := c.Request().Context()

	tasks, err := taskschema.FindBy(ctx, taskschema.Task{
		Status: "pending",
	})
	if err != nil {
		if err == sql.ErrNoRows {
			return c.JSON(http.StatusOK, nil)
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to fetch pending task: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, tasks)
}

func Register(g *echo.Group) {
	h := NewHandler()

	g.GET("", h.Index)
	g.GET("/:pid", h.Show)
	g.POST("", h.Create)
}
