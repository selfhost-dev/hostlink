package gorm

import (
	"context"
	"fmt"
	"hostlink/domain/task"

	"github.com/oklog/ulid/v2"
	"gorm.io/gorm"
)

type TaskRepository struct {
	db *gorm.DB
}

func NewTaskRepository(db *gorm.DB) task.Repository {
	return &TaskRepository{db: db}
}

func (r *TaskRepository) Create(ctx context.Context, t *task.Task) error {
	t.ID = "tsk_" + ulid.Make().String()
	t.Status = "pending"
	return r.db.WithContext(ctx).Create(t).Error
}

func (r *TaskRepository) FindAll(ctx context.Context) ([]task.Task, error) {
	var tasks []task.Task
	err := r.db.WithContext(ctx).Order("created_at desc").Find(&tasks).Error
	return tasks, err
}

func (r *TaskRepository) FindByStatus(ctx context.Context, status string) ([]task.Task, error) {
	var tasks []task.Task
	err := r.db.WithContext(ctx).Where("status = ?", status).Find(&tasks).Error
	return tasks, err
}

func (r *TaskRepository) FindByID(ctx context.Context, id string) (*task.Task, error) {
	var t task.Task
	err := r.db.WithContext(ctx).First(&t, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *TaskRepository) Update(ctx context.Context, t *task.Task) error {
	var existing task.Task
	if err := r.db.WithContext(ctx).First(&existing, "id = ?", t.ID).Error; err != nil {
		return fmt.Errorf("task not found")
	}

	return r.db.WithContext(ctx).Save(t).Error
}
