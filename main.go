package main

import (
	"database/sql"
	_ "embed"
	"hostlink/app"
	"hostlink/internal/agent"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/mattn/go-shellwords"
)

//go:embed static/index.html
var indexHTML string

// TaskRequest represents the incoming command
type TaskRequest struct {
	Command  string `json:"command"`
	Priority int    `json:"priority"`
}

// CommandResponse represents the incoming command
type CommandResponse struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error"`
	Code    int    `json:"exit_code"`
}

func main() {
	cfg := app.NewConfig().WithDBURL("file:hostlink.db")
	application := app.New(cfg)

	err := application.Start()
	if err != nil {
		log.Fatal("Failed to start the application:", err)
	}
	defer application.Stop()

	e := echo.New()

	// Add middleware for logging and recovery
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.GET("/", func(c echo.Context) error {
		return c.HTML(http.StatusOK, indexHTML)
	})
	// Health check endpoint
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "healthy"})
	})

	// Command execution endpoint
	e.POST("/execute", executeCommand)

	// Subnet a new command
	e.POST("/commands", func(c echo.Context) error {
		var req TaskRequest
		if err := c.Bind(&req); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		// Parse the command string with shellwords
		if req.Command == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "command is required"})
		}

		_, err := shellwords.Parse(req.Command)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "Invalid command syntax: " + err.Error(),
			})
		}

		task := app.Task{
			PID:      uuid.New().String(),
			Command:  req.Command,
			Priority: req.Priority,
		}
		ctx := c.Request().Context()

		err = application.InsertTask(ctx, task)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "Failed to save command: " + err.Error(),
			})
		}

		cmd := &app.Task{
			PID:       task.PID,
			Command:   req.Command,
			Status:    "pending",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		return c.JSON(http.StatusOK, cmd)
	})

	// Get all commands
	e.GET("/commands", func(c echo.Context) error {
		ctx := c.Request().Context()

		tasks, err := application.GetAllTasks(ctx)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "Failed to fetch tasks: " + err.Error(),
			})
		}

		return c.JSON(http.StatusOK, tasks)
	})

	e.GET("/commands/pending", func(c echo.Context) error {
		ctx := c.Request().Context()

		task, err := application.GetPendingTask(ctx)
		if err != nil {
			if err == sql.ErrNoRows {
				return c.JSON(http.StatusOK, nil)
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "Failed to fetch pending task: " + err.Error(),
			})
		}

		cmd := &app.Task{
			ID:      task.ID,
			Command: task.Command,
			Status:  "pending",
		}

		return c.JSON(http.StatusOK, cmd)
	})

	e.PUT("/commands/:pid", func(c echo.Context) error {
		pid := c.Param("pid")

		update := app.Task{
			PID: pid,
		}

		if err := c.Bind(&update); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		ctx := c.Request().Context()
		err = application.UpdateTask(
			ctx,
			update,
		)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "Failed to update task: " + err.Error(),
			})
		}
		return c.JSON(http.StatusOK, map[string]string{
			"status": "updated",
		})
	})

	go agent.StartAgent(
		application.GetPendingTask,
		application.UpdateTask,
	)

	log.Fatal(e.Start(":1323"))
}

func executeCommand(c echo.Context) error {
	var req TaskRequest

	// Parse the request body
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, CommandResponse{
			Success: false,
			Error:   "Invalid request format",
		})
	}

	// Validate command is not empty
	if req.Command == "" {
		return c.JSON(http.StatusBadRequest, CommandResponse{
			Success: false,
			Error:   "Command cannot be empty",
		})
	}

	// Parse command with shellwords to validate syntax
	parts, err := shellwords.Parse(req.Command)
	if err != nil {
		return c.JSON(http.StatusBadRequest, CommandResponse{
			Success: false,
			Error:   "Invalid command syntax: " + err.Error(),
		})
	}

	if len(parts) == 0 {
		return c.JSON(http.StatusBadRequest, CommandResponse{
			Success: false,
			Error:   "Empty command",
		})
	}

	cmd := exec.Command("/bin/sh", "-c", req.Command)

	output, err := cmd.CombinedOutput()

	response := CommandResponse{
		Success: err == nil,
		Output:  strings.TrimSpace(string(output)),
	}

	if err != nil {
		response.Error = err.Error()
		if exitError, ok := err.(*exec.ExitError); ok {
			response.Code = exitError.ExitCode()
		}
		return c.JSON(http.StatusOK, response)
	}

	return c.JSON(http.StatusOK, response)
}
