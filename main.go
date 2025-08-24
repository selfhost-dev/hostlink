package main

import (
	"database/sql"
	_ "embed"
	"hostlink/app"
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

// CommandRequest represents the incoming command
type CommandRequest struct {
	Command string `json:"command"`
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
		var req CommandRequest
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

		id := uuid.New().String()
		ctx := c.Request().Context()

		err = application.InsertTask(ctx, id, req.Command)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "Failed to save command: " + err.Error(),
			})
		}

		cmd := &app.Command{
			ID:        uuid.New().String(),
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

		id, command, err := application.GetPendingTask(ctx)
		if err != nil {
			if err == sql.ErrNoRows {
				return c.JSON(http.StatusOK, nil)
			}
			return c.JSON(http.StatusInternalServerError, map[string]string{
				"error": "Failed to fetch pending task: " + err.Error(),
			})
		}

		cmd := &app.Command{
			ID:      id,
			Command: command,
			Status:  "pending",
		}

		return c.JSON(http.StatusOK, cmd)
	})

	e.PUT("/commands/:id", func(c echo.Context) error {
		id := c.Param("id")

		var update struct {
			Status   string `json:"status"`
			Output   string `json:"output"`
			Error    string `json:"error"`
			ExitCode int    `json:"exit_code"`
		}

		if err := c.Bind(&update); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		ctx := c.Request().Context()
		err = application.UpdateTaskStatus(
			ctx,
			id,
			update.Status,
			update.Output,
			update.Error,
			update.ExitCode,
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

	go startAgent()
	log.Fatal(e.Start(":1323"))
}

func executeCommand(c echo.Context) error {
	var req CommandRequest

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
