package task

import "context"

type Repository interface {
	Create(ctx context.Context, task *Task) error
	FindAll(ctx context.Context, filters TaskFilters) ([]Task, error)
	FindByStatus(ctx context.Context, status string) ([]Task, error)
	FindByID(ctx context.Context, id string) (*Task, error)
	Update(ctx context.Context, task *Task) error
}
