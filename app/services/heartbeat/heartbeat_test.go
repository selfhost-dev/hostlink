package heartbeat

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockAPIServer struct {
	mock.Mock
}

func (m *MockAPIServer) Heartbeat(ctx context.Context, agentID string) error {
	args := m.Called(ctx, agentID)
	return args.Error(0)
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
	apiserver := new(MockAPIServer)
	agentstate := new(MockAgentState)
	service := NewWithDependencies(apiserver, agentstate)
	return service, apiserver, agentstate
}

// TestSend_NoAgentID - returns error when agent is not registered
func TestSend_NoAgentID(t *testing.T) {
	service, _, agentstate := setupTestService()

	agentstate.On("GetAgentID").Return("")

	err := service.Send()

	assert.EqualError(t, err, "agent not registered: missing agent ID")
	agentstate.AssertExpectations(t)
}

// TestSend_Success - sends heartbeat successfully
func TestSend_Success(t *testing.T) {
	service, apiserver, agentstate := setupTestService()

	agentstate.On("GetAgentID").Return("agent-123")
	apiserver.On("Heartbeat", mock.Anything, "agent-123").Return(nil)

	err := service.Send()

	assert.NoError(t, err)
	agentstate.AssertExpectations(t)
	apiserver.AssertExpectations(t)
}

// TestSend_APIError - returns error when API call fails
func TestSend_APIError(t *testing.T) {
	service, apiserver, agentstate := setupTestService()
	expectedErr := errors.New("connection refused")

	agentstate.On("GetAgentID").Return("agent-123")
	apiserver.On("Heartbeat", mock.Anything, "agent-123").Return(expectedErr)

	err := service.Send()

	assert.Equal(t, expectedErr, err)
	agentstate.AssertExpectations(t)
	apiserver.AssertExpectations(t)
}

// TestSend_UsesBackgroundContext - verifies context.Background is used
func TestSend_UsesBackgroundContext(t *testing.T) {
	service, apiserver, agentstate := setupTestService()

	agentstate.On("GetAgentID").Return("agent-123")
	apiserver.On("Heartbeat", mock.MatchedBy(func(ctx context.Context) bool {
		_, hasDeadline := ctx.Deadline()
		return !hasDeadline && ctx.Err() == nil
	}), "agent-123").Return(nil)

	err := service.Send()

	assert.NoError(t, err)
	apiserver.AssertExpectations(t)
}
