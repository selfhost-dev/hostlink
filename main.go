package main

import (
	_ "embed"
	"log"
	"net/http"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

//go:embed index.html
var indexHTML string

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

// Command represents a command in the queue
type Command struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	Args      []string  `json:"args,omitempty"`
	Status    string    `json:"status"` // pending, running, completed
	Output    string    `json:"output"`
	Error     string    `json:"error"`
	ExitCode  int       `json:"exit_code,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// In-memory storage
var (
	commands = make(map[string]*Command)
	mu       sync.RWMutex
)

func main() {
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

		cmd := &Command{
			ID:        uuid.New().String(),
			Command:   req.Command,
			Args:      req.Args,
			Status:    "pending",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		mu.Lock()
		commands[cmd.ID] = cmd
		mu.Unlock()

		return c.JSON(http.StatusOK, cmd)
	})

	// Get all commands
	e.GET("/commands", func(c echo.Context) error {
		mu.RLock()
		defer mu.RUnlock()

		cmdList := make([]*Command, 0, len(commands))
		for _, cmd := range commands {
			cmdList = append(cmdList, cmd)
		}

		sort.Slice(cmdList, func(i, j int) bool {
			return cmdList[i].CreatedAt.After(cmdList[j].CreatedAt)
		})

		return c.JSON(http.StatusOK, cmdList)
	})

	e.GET("/commands/pending", func(c echo.Context) error {
		mu.RLock()
		defer mu.RUnlock()

		for _, cmd := range commands {
			if cmd.Status == "pending" {
				return c.JSON(http.StatusOK, cmd)
			}
		}

		return c.JSON(http.StatusOK, nil)
	})

	e.PUT("/commands/:id", func(c echo.Context) error {
		id := c.Param("id")

		var update struct {
			Status   string `json:"status"`
			Output   string `json:"output"`
			Error    string `json:"error"`
			ExitCode int    `json:"exit_codd"`
		}

		if err := c.Bind(&update); err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request"})
		}

		mu.Lock()
		defer mu.Unlock()

		cmd, exists := commands[id]
		if !exists {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "Command not found"})
		}

		cmd.Status = update.Status
		cmd.Output = update.Output
		cmd.Error = update.Error
		cmd.ExitCode = update.ExitCode
		cmd.UpdatedAt = time.Now()

		return c.JSON(http.StatusOK, cmd)
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
