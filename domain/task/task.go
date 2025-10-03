package task

import (
	"time"
)

type Task struct {
	ID        string     `json:"id"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
	Command   string     `json:"command"`
	Status    string     `json:"status"`
	Priority  int        `json:"priority"`
	Output    string     `json:"output"`
	Error     string     `json:"error"`
	ExitCode  int        `json:"exit_code"`
}

type TaskFilters struct {
	Status   *string
	Priority *int
}
