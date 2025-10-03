package tasks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"hostlink/domain/task"
	"hostlink/internal/validator"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTaskRepository struct {
	createFunc   func(ctx context.Context, t *task.Task) error
	findAllFunc  func(ctx context.Context, tf task.TaskFilters) ([]task.Task, error)
	findByIDFunc func(ctx context.Context, id string) (*task.Task, error)
}

func (m *mockTaskRepository) Create(ctx context.Context, t *task.Task) error {
	if m.createFunc != nil {
		return m.createFunc(ctx, t)
	}
	return nil
}

func (m *mockTaskRepository) FindAll(ctx context.Context, tf task.TaskFilters) ([]task.Task, error) {
	if m.findAllFunc != nil {
		return m.findAllFunc(ctx, tf)
	}
	return []task.Task{}, nil
}

func (m *mockTaskRepository) FindByStatus(ctx context.Context, status string) ([]task.Task, error) {
	return []task.Task{}, nil
}

func (m *mockTaskRepository) FindByID(ctx context.Context, id string) (*task.Task, error) {
	if m.findByIDFunc != nil {
		return m.findByIDFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockTaskRepository) Update(ctx context.Context, t *task.Task) error {
	return nil
}

func TestHandler_Create(t *testing.T) {
	t.Run("should create task successfully", func(t *testing.T) {
		repo := &mockTaskRepository{
			createFunc: func(ctx context.Context, tsk *task.Task) error {
				tsk.ID = "tsk_123"
				tsk.Status = "pending"
				return nil
			},
		}
		handler := NewHandler(repo)

		e := echo.New()
		e.Validator = validator.New()

		reqBody := TaskRequest{
			Command:  "echo hello",
			Priority: 1,
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := handler.Create(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, rec.Code)

		var response TaskResponse
		json.Unmarshal(rec.Body.Bytes(), &response)
		assert.Equal(t, "tsk_123", response.ID)
		assert.Equal(t, "echo hello", response.Command)
		assert.Equal(t, 1, response.Priority)
	})

	t.Run("should return 400 when command is missing", func(t *testing.T) {
		repo := &mockTaskRepository{}
		handler := NewHandler(repo)

		e := echo.New()
		e.Validator = validator.New()

		reqBody := TaskRequest{
			Priority: 1,
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := handler.Create(c)
		require.Error(t, err)

		he, ok := err.(*echo.HTTPError)
		require.True(t, ok)
		assert.Equal(t, http.StatusBadRequest, he.Code)
	})

	t.Run("should return 400 when command syntax is invalid", func(t *testing.T) {
		repo := &mockTaskRepository{}
		handler := NewHandler(repo)

		e := echo.New()
		e.Validator = validator.New()

		reqBody := TaskRequest{
			Command:  "echo 'unclosed quote",
			Priority: 1,
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := handler.Create(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("should return 500 when repository fails", func(t *testing.T) {
		repo := &mockTaskRepository{
			createFunc: func(ctx context.Context, tsk *task.Task) error {
				return errors.New("database error")
			},
		}
		handler := NewHandler(repo)

		e := echo.New()
		e.Validator = validator.New()

		reqBody := TaskRequest{
			Command:  "echo hello",
			Priority: 1,
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := handler.Create(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})

	t.Run("should return 400 when request body is invalid JSON", func(t *testing.T) {
		repo := &mockTaskRepository{}
		handler := NewHandler(repo)

		e := echo.New()
		e.Validator = validator.New()

		req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader([]byte("invalid json")))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := handler.Create(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})
}

func TestHandler_Get(t *testing.T) {
	t.Run("should return task successfully", func(t *testing.T) {
		expectedTask := &task.Task{
			ID:       "tsk_123",
			Command:  "ls -la",
			Status:   "completed",
			Priority: 1,
			Output:   "total 48\ndrwxr-xr-x",
			ExitCode: 0,
		}

		repo := &mockTaskRepository{
			findByIDFunc: func(ctx context.Context, id string) (*task.Task, error) {
				return expectedTask, nil
			},
		}
		handler := NewHandler(repo)

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/tasks/tsk_123", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("tsk_123")

		err := handler.Get(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response task.Task
		json.Unmarshal(rec.Body.Bytes(), &response)
		assert.Equal(t, "tsk_123", response.ID)
		assert.Equal(t, "ls -la", response.Command)
		assert.Equal(t, "completed", response.Status)
		assert.Equal(t, 1, response.Priority)
		assert.Equal(t, "total 48\ndrwxr-xr-x", response.Output)
		assert.Equal(t, 0, response.ExitCode)
	})

	t.Run("should return 404 when task not found", func(t *testing.T) {
		repo := &mockTaskRepository{
			findByIDFunc: func(ctx context.Context, id string) (*task.Task, error) {
				return nil, nil
			},
		}
		handler := NewHandler(repo)

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/tasks/nonexistent", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("nonexistent")

		err := handler.Get(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("should return 500 when repository fails", func(t *testing.T) {
		repo := &mockTaskRepository{
			findByIDFunc: func(ctx context.Context, id string) (*task.Task, error) {
				return nil, errors.New("database error")
			},
		}
		handler := NewHandler(repo)

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/tasks/tsk_123", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("tsk_123")

		err := handler.Get(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusInternalServerError, rec.Code)
	})
}
