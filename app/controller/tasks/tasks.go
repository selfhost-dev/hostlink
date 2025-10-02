// Package tasks handles all the command requests
package tasks

import (
	"errors"
	"fmt"
	"hostlink/domain/task"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/mattn/go-shellwords"
	"gorm.io/gorm"
)

type (
	Handler struct {
		repo task.Repository
	}
	OkCommand struct {
		Command string `json:"command"`
	}
	TaskRequest struct {
		Command  string `json:"command" validate:"required"`
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

	if err := c.Validate(&req); err != nil {
		return err
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

	var filters task.TaskFilters

	if status := c.QueryParam("status"); status != "" {
		filters.Status = &status
	}

	if priorityStr := c.QueryParam("priority"); priorityStr != "" {
		var priority int
		if _, err := fmt.Sscanf(priorityStr, "%d", &priority); err == nil {
			filters.Priority = &priority
		}
	}

	tasks, err := h.repo.FindAll(ctx, filters)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to fetch tasks: " + err.Error(),
		})
	}

	return c.JSON(http.StatusOK, tasks)
}

func (h Handler) Get(c echo.Context) error {
	ctx := c.Request().Context()
	taskID := c.Param("id")

	task, err := h.repo.FindByID(ctx, taskID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return c.JSON(http.StatusNotFound, map[string]string{
				"error": "Task not found",
			})
		}
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": "Failed to fetch task: " + err.Error(),
		})
	}

	if task == nil {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "Task not found",
		})
	}

	return c.JSON(http.StatusOK, task)
}

func (h *Handler) RegisterRoutes(g *echo.Group) {
	g.POST("", h.Create)
	g.GET("", h.Index)
	g.GET("/:id", h.Get)
}
