package gorm

import (
	"context"
	"fmt"
	"testing"

	"hostlink/domain/task"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestTaskRepository_Create(t *testing.T) {
	t.Run("creates task with generated ID", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		newTask := &task.Task{
			Command:  "echo 'test'",
			Priority: 1,
		}

		err := repo.Create(context.Background(), newTask)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if newTask.ID == "" {
			t.Error("Expected ID to be generated")
		}

		if newTask.Status != "pending" {
			t.Errorf("Expected status to be 'pending', got: %s", newTask.Status)
		}
	})

	t.Run("sets default status to pending", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		newTask := &task.Task{
			Command:  "ls -la",
			Priority: 2,
		}

		err := repo.Create(context.Background(), newTask)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if newTask.Status != "pending" {
			t.Errorf("Expected status to be 'pending', got: %s", newTask.Status)
		}
	})
}

func TestTaskRepository_FindAll(t *testing.T) {
	t.Run("returns all tasks ordered by created_at desc", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		task1 := &task.Task{Command: "task1", Priority: 1}
		task2 := &task.Task{Command: "task2", Priority: 2}
		task3 := &task.Task{Command: "task3", Priority: 3}

		repo.Create(context.Background(), task1)
		repo.Create(context.Background(), task2)
		repo.Create(context.Background(), task3)

		tasks, err := repo.FindAll(context.Background())
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(tasks) != 3 {
			t.Errorf("Expected 3 tasks, got: %d", len(tasks))
		}

		if tasks[0].Command != "task3" {
			t.Error("Expected tasks to be ordered by created_at desc")
		}
	})

	t.Run("returns empty slice when no tasks exist", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		tasks, err := repo.FindAll(context.Background())
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(tasks) != 0 {
			t.Errorf("Expected 0 tasks, got: %d", len(tasks))
		}
	})
}

func TestTaskRepository_FindByStatus(t *testing.T) {
	t.Run("returns tasks with matching status", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		task1 := &task.Task{Command: "task1", Priority: 1}
		task2 := &task.Task{Command: "task2", Priority: 2}

		repo.Create(context.Background(), task1)
		repo.Create(context.Background(), task2)

		task1.Status = "completed"
		repo.Update(context.Background(), task1)

		pendingTasks, err := repo.FindByStatus(context.Background(), "pending")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(pendingTasks) != 1 {
			t.Errorf("Expected 1 pending task, got: %d", len(pendingTasks))
		}

		if pendingTasks[0].Command != "task2" {
			t.Error("Expected to find task2")
		}
	})

	t.Run("returns empty slice when no tasks match status", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		tasks, err := repo.FindByStatus(context.Background(), "running")
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if len(tasks) != 0 {
			t.Errorf("Expected 0 tasks, got: %d", len(tasks))
		}
	})
}

func TestTaskRepository_FindByID(t *testing.T) {
	t.Run("returns task with matching ID", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		newTask := &task.Task{Command: "test command", Priority: 1}
		repo.Create(context.Background(), newTask)

		foundTask, err := repo.FindByID(context.Background(), newTask.ID)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if foundTask.Command != "test command" {
			t.Errorf("Expected command 'test command', got: %s", foundTask.Command)
		}
	})

	t.Run("returns error when task not found", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		_, err := repo.FindByID(context.Background(), "nonexistent_id")
		if err == nil {
			t.Error("Expected error for nonexistent ID, got nil")
		}
	})
}

func TestTaskRepository_Update(t *testing.T) {
	t.Run("updates task fields", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		newTask := &task.Task{Command: "original", Priority: 1}
		repo.Create(context.Background(), newTask)

		newTask.Status = "completed"
		newTask.Output = "success"
		newTask.ExitCode = 0

		err := repo.Update(context.Background(), newTask)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		updatedTask, _ := repo.FindByID(context.Background(), newTask.ID)
		if updatedTask.Status != "completed" {
			t.Errorf("Expected status 'completed', got: %s", updatedTask.Status)
		}
		if updatedTask.Output != "success" {
			t.Errorf("Expected output 'success', got: %s", updatedTask.Output)
		}
	})

	t.Run("returns error when task does not exist", func(t *testing.T) {
		db := setupTaskTestDB(t)
		repo := NewTaskRepository(db)

		nonExistentTask := &task.Task{
			ID:      "nonexistent_id",
			Command: "nonexistent",
		}

		err := repo.Update(context.Background(), nonExistentTask)
		if err == nil {
			t.Error("Expected error for nonexistent task, got nil")
		}
	})
}

func setupTaskTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	dbName := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dbName), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to connect to database: %v", err)
	}

	err = db.AutoMigrate(&task.Task{})
	if err != nil {
		t.Fatalf("Failed to migrate: %v", err)
	}

	return db
}
