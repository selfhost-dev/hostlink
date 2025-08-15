package main

import (
	"log"
	"net/http"
	"os/exec"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// CommandRequest represents the incoming command
type CommandRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// CommandResponse represents the incoming command
type CommandResponse struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Error   string `json:"error"`
	Code    int    `json:"exit_code"`
}

func main() {
	e := echo.New()

	// Add middleware for logging and recovery
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	// Health check endpoint
	e.GET("/health", func(c echo.Context) error {
		return c.JSON(http.StatusOK, map[string]string{"status": "healthy"})
	})

	// Command execution endpoint
	e.POST("/execute", executeCommand)

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

	// Build the command string
	fullCommand := req.Command
	if len(req.Args) > 0 {
		// Join command and args into a single string for shell execution
		fullCommand = req.Command + " " + strings.Join(req.Args, " ")
	}

	// Execute through /bin/sh -c
	cmd := exec.Command("/bin/sh", "-c", fullCommand)

	// Execute theo command and capture output
	output, err := cmd.CombinedOutput()

	// Prepare response
	response := CommandResponse{
		Success: err == nil,
		Output:  strings.TrimSpace(string(output)),
	}

	if err != nil {
		response.Error = err.Error()
		// Try to get exit code
		if exitError, ok := err.(*exec.ExitError); ok {
			response.Code = exitError.ExitCode()
		}
		return c.JSON(http.StatusOK, response) // Still return 200 OK but with error in response
	}

	return c.JSON(http.StatusOK, response)
}
