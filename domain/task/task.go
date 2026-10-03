package task

import (
	"time"
)

type Task struct {
	ID                 string     `json:"id"`
	ExecutionAttemptID string     `json:"execution_attempt_id"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
	DeletedAt          *time.Time `json:"deleted_at,omitempty"`
	Command            string     `json:"command"`
	Status             string     `json:"status"`
	Priority           int        `json:"priority"`
	Output             string     `json:"output"`
	Error              string     `json:"error"`
	ExitCode           int        `json:"exit_code"`
	// SealedEnv carries the task's secrets (selfhost #3168), sealed to this agent's key; they
	// reach the script as environment variables and never appear in Command.
	SealedEnv string `json:"sealed_env,omitempty"`
}

type TaskFilters struct {
	Status   *string
	Priority *int
}
