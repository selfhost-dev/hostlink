package task

import (
	"time"
)

type Task struct {
	ID        string
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time
	Command   string
	Status    string
	Priority  int
	Output    string
	Error     string
	ExitCode  int
}

type TaskFilters struct {
	Status   *string
	Priority *int
}
