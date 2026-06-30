package heartbeat

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"hostlink/domain/task"
	"hostlink/internal/apiserver"
)

type MockAPIServer struct {
	mock.Mock
}

func (m *MockAPIServer) Heartbeat(ctx context.Context, agentID string) (*apiserver.HeartbeatResponse, error) {
	args := m.Called(ctx, agentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*apiserver.HeartbeatResponse), args.Error(1)
}

type MockAgentState struct {
	mock.Mock
}

func (m *MockAgentState) Save() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAgentState) Load() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAgentState) GetAgentID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockAgentState) SetAgentID(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockAgentState) Clear() error {
	args := m.Called()
	return args.Error(0)
}

func setupTestService() (*heartbeatService, *MockAPIServer, *MockAgentState) {
	mockSvr := new(MockAPIServer)
	agentstate := new(MockAgentState)
	service := NewWithDependencies(mockSvr, agentstate)
	return service, mockSvr, agentstate
}

// TestSend_NoAgentID - returns error when agent is not registered
func TestSend_NoAgentID(t *testing.T) {
	service, _, agentstate := setupTestService()

	agentstate.On("GetAgentID").Return("")

	_, err := service.Send()

	assert.EqualError(t, err, "agent not registered: missing agent ID")
	agentstate.AssertExpectations(t)
}

// TestSend_Success - sends heartbeat successfully
func TestSend_Success(t *testing.T) {
	service, mockSvr, agentstate := setupTestService()

	agentstate.On("GetAgentID").Return("agent-123")
	mockSvr.On("Heartbeat", mock.Anything, "agent-123").Return(&apiserver.HeartbeatResponse{}, nil)

	tasks, err := service.Send()

	assert.NoError(t, err)
	assert.Empty(t, tasks)
	agentstate.AssertExpectations(t)
	mockSvr.AssertExpectations(t)
}

// TestSend_APIError - returns error when API call fails
func TestSend_APIError(t *testing.T) {
	service, mockSvr, agentstate := setupTestService()
	expectedErr := errors.New("connection refused")

	agentstate.On("GetAgentID").Return("agent-123")
	mockSvr.On("Heartbeat", mock.Anything, "agent-123").Return(nil, expectedErr)

	tasks, err := service.Send()

	assert.Nil(t, tasks)
	assert.Equal(t, expectedErr, err)
	agentstate.AssertExpectations(t)
	mockSvr.AssertExpectations(t)
}

// TestSend_UsesBackgroundContext - verifies context.Background is used
func TestSend_UsesBackgroundContext(t *testing.T) {
	service, mockSvr, agentstate := setupTestService()

	agentstate.On("GetAgentID").Return("agent-123")
	mockSvr.On("Heartbeat", mock.MatchedBy(func(ctx context.Context) bool {
		_, hasDeadline := ctx.Deadline()
		return !hasDeadline && ctx.Err() == nil
	}), "agent-123").Return(&apiserver.HeartbeatResponse{}, nil)

	tasks, err := service.Send()

	assert.NoError(t, err)
	assert.Empty(t, tasks)
	mockSvr.AssertExpectations(t)
}

// TestSend_WithPendingTasks - returns pending tasks from heartbeat response
func TestSend_WithPendingTasks(t *testing.T) {
	service, mockSvr, agentstate := setupTestService()

	agentstate.On("GetAgentID").Return("agent-123")
	resp := &apiserver.HeartbeatResponse{
		Message: "ok",
		PendingTasks: []task.Task{
			{ID: "tsk_test", Command: "echo hello", Status: "pending"},
		},
	}
	mockSvr.On("Heartbeat", mock.Anything, "agent-123").Return(resp, nil)

	pendingTasks, err := service.Send()

	assert.NoError(t, err)
	assert.Len(t, pendingTasks, 1)
	assert.Equal(t, "tsk_test", pendingTasks[0].ID)
	assert.Equal(t, "echo hello", pendingTasks[0].Command)
}
