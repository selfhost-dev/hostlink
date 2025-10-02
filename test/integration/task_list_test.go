//go:build integration
// +build integration

package integration

import (
	"context"
	"encoding/json"
	"hostlink/domain/task"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskList(t *testing.T) {
	t.Run("lists all tasks without filters", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)

		task1 := &task.Task{Command: "task1", Priority: 1}
		task2 := &task.Task{Command: "task2", Priority: 2}
		container.TaskRepository.Create(context.Background(), task1)
		container.TaskRepository.Create(context.Background(), task2)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var tasks []task.Task
		err := json.Unmarshal(rec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(tasks), 2)
	})

	t.Run("filters tasks by status pending", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)

		task1 := &task.Task{Command: "task1", Priority: 1}
		task2 := &task.Task{Command: "task2", Priority: 2}
		container.TaskRepository.Create(context.Background(), task1)
		container.TaskRepository.Create(context.Background(), task2)

		task1.Status = "completed"
		container.TaskRepository.Update(context.Background(), task1)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks?status=pending", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var tasks []task.Task
		err := json.Unmarshal(rec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.Equal(t, 1, len(tasks))
		assert.Equal(t, "task2", tasks[0].Command)
		assert.Equal(t, "pending", tasks[0].Status)
	})

	t.Run("filters tasks by status completed", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)

		task1 := &task.Task{Command: "task1", Priority: 1}
		task2 := &task.Task{Command: "task2", Priority: 2}
		container.TaskRepository.Create(context.Background(), task1)
		container.TaskRepository.Create(context.Background(), task2)

		task1.Status = "completed"
		container.TaskRepository.Update(context.Background(), task1)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks?status=completed", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var tasks []task.Task
		err := json.Unmarshal(rec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.Equal(t, 1, len(tasks))
		assert.Equal(t, "completed", tasks[0].Status)
	})

	t.Run("filters tasks by status running", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)

		task1 := &task.Task{Command: "task1", Priority: 1}
		container.TaskRepository.Create(context.Background(), task1)

		task1.Status = "running"
		container.TaskRepository.Update(context.Background(), task1)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks?status=running", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var tasks []task.Task
		err := json.Unmarshal(rec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.Equal(t, 1, len(tasks))
		assert.Equal(t, "running", tasks[0].Status)
	})

	t.Run("filters tasks by status failed", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)

		task1 := &task.Task{Command: "task1", Priority: 1}
		container.TaskRepository.Create(context.Background(), task1)

		task1.Status = "failed"
		container.TaskRepository.Update(context.Background(), task1)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks?status=failed", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var tasks []task.Task
		err := json.Unmarshal(rec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.Equal(t, 1, len(tasks))
		assert.Equal(t, "failed", tasks[0].Status)
	})

	t.Run("filters tasks by priority", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)

		task1 := &task.Task{Command: "task1", Priority: 1}
		task2 := &task.Task{Command: "task2", Priority: 2}
		task3 := &task.Task{Command: "task3", Priority: 2}
		container.TaskRepository.Create(context.Background(), task1)
		container.TaskRepository.Create(context.Background(), task2)
		container.TaskRepository.Create(context.Background(), task3)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks?priority=2", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var tasks []task.Task
		err := json.Unmarshal(rec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.Equal(t, 2, len(tasks))
	})

	t.Run("combines status and priority filters", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)

		task1 := &task.Task{Command: "task1", Priority: 1}
		task2 := &task.Task{Command: "task2", Priority: 2}
		task3 := &task.Task{Command: "task3", Priority: 2}
		container.TaskRepository.Create(context.Background(), task1)
		container.TaskRepository.Create(context.Background(), task2)
		container.TaskRepository.Create(context.Background(), task3)

		task2.Status = "completed"
		container.TaskRepository.Update(context.Background(), task2)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks?status=pending&priority=2", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var tasks []task.Task
		err := json.Unmarshal(rec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.Equal(t, 1, len(tasks))
		assert.Equal(t, "task3", tasks[0].Command)
	})

	t.Run("returns empty array when no tasks match filters", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)

		task1 := &task.Task{Command: "task1", Priority: 1}
		container.TaskRepository.Create(context.Background(), task1)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks?status=completed", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var tasks []task.Task
		err := json.Unmarshal(rec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.Equal(t, 0, len(tasks))
	})

	t.Run("returns tasks sorted by created_at desc", func(t *testing.T) {
		e, container := setupTaskTestEnvironment(t)

		task1 := &task.Task{Command: "first", Priority: 1}
		task2 := &task.Task{Command: "second", Priority: 2}
		task3 := &task.Task{Command: "third", Priority: 3}
		container.TaskRepository.Create(context.Background(), task1)
		container.TaskRepository.Create(context.Background(), task2)
		container.TaskRepository.Create(context.Background(), task3)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks", nil)
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var tasks []task.Task
		err := json.Unmarshal(rec.Body.Bytes(), &tasks)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(tasks), 3)
		assert.Equal(t, "third", tasks[0].Command)
	})
}
