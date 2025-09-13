// Package taskschema holds the schema for the task
package taskschema

import (
	"context"
	"hostlink/internal/dbconn"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

var db *gorm.DB

// Task represents a command in the queue
type Task struct {
	gorm.Model
	PID      string `json:"pid"`
	Command  string `json:"command"`
	Status   string `json:"status"` // pending, running, completed
	Priority int    `json:"priority"`
	Output   string `json:"output"`
	Error    string `json:"error"`
	ExitCode int    `json:"exit_code,omitempty"`
}

func New() *Task {
	return &Task{}
}

func Create(ctx context.Context, task Task) error {
	result := db.WithContext(ctx).Create(task)
	return result.Error
}

func All(ctx context.Context) ([]Task, error) {
	var tasks []Task
	result := db.WithContext(ctx).Order("created_at desc").Find(&tasks)

	return tasks, result.Error
}

func Last(ctx context.Context, taskQuery Task) (Task, error) {
	var task Task
	result := db.WithContext(ctx).Last(&task)

	return task, result.Error
}

func FindOne(ctx context.Context, taskQuery Task) (Task, error) {
	var task Task
	result := db.WithContext(ctx).Where(&taskQuery).First(&task)

	return task, result.Error
}

func FindBy(ctx context.Context, task Task) ([]Task, error) {
	var tasks []Task
	result := db.WithContext(ctx).Where(&task).Find(&tasks)
	return tasks, result.Error
}

func (t *Task) Save(ctx context.Context) error {
	res := db.WithContext(ctx).Save(t)
	return res.Error
}

func (t *Task) BeforeCreate(tx *gorm.DB) (err error) {
	t.PID = generatePID()
	t.Status = "pending"
	return
}

func generatePID() string {
	return "tsk_" + ulid.Make().String()
}

func init() {
	var err error
	db, err = dbconn.GetConn()
	if err != nil {
		panic(err)
	}
}
