// Package tasks handles all the command requests
package tasks

import (
	"hostlink/domain/task"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mattn/go-shellwords"
)

type (
	Handler struct {
		repo task.Repository
	}
	OkCommand struct {
		Command string `json:"command"`
	}
	TaskRequest struct {
		Command  string `json:"command"`
		Priority int    `json:"priority"`
	}
)

func NewHandler(repo task.Repository) *Handler {
	return &Handler{repo: repo}
}

func (h Handler) Create(c echo.Context) error {
	var req TaskRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
	}

	_, err := shellwords.Parse(req.Command)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "Invalid command syntax: " + err.Error(),
		})
	}

	ctx := c.Request().Context()

	newTask := &task.Task{
		Command:  req.Command,
		Priority: req.Priority,
	}

	err = h.repo.Create(ctx, newTask)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to save command: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, newTask)
}

func (h Handler) Index(c echo.Context) error {
	ctx := c.Request().Context()

	tasks, err := h.repo.FindAll(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to fetch tasks: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, tasks)
}

func (h Handler) Show(c echo.Context) error {
	ctx := c.Request().Context()

	tasks, err := h.repo.FindByStatus(ctx, "pending")
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to fetch pending task: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, tasks)
}

func (h *Handler) RegisterRoutes(g *echo.Group) {
	g.GET("", h.Index)
	g.GET("/:pid", h.Show)
	g.POST("", h.Create)
}
