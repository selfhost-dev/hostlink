package heartbeat

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"hostlink/domain/task"
	"hostlink/internal/apiserver"
	"hostlink/internal/slowrequest"
	"hostlink/internal/testutil"
)

type MockAPIServer struct {
	mock.Mock
}

func (m *MockAPIServer) Heartbeat(ctx context.Context, agentID string, slow []slowrequest.Count) (*apiserver.HeartbeatResponse, error) {
	args := m.Called(ctx, agentID, slow)
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
	// Fresh counts: other tests in the package make slow requests that land
	// in slowrequest.Default.
	service.slowCounts = slowrequest.NewCounts()
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
	mockSvr.On("Heartbeat", mock.Anything, "agent-123", noSlowRequests).Return(&apiserver.HeartbeatResponse{}, nil)

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
	mockSvr.On("Heartbeat", mock.Anything, "agent-123", noSlowRequests).Return(nil, expectedErr)

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
	}), "agent-123", noSlowRequests).Return(&apiserver.HeartbeatResponse{}, nil)

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
	mockSvr.On("Heartbeat", mock.Anything, "agent-123", noSlowRequests).Return(resp, nil)

	pendingTasks, err := service.Send()

	assert.NoError(t, err)
	assert.Len(t, pendingTasks, 1)
	assert.Equal(t, "tsk_test", pendingTasks[0].ID)
	assert.Equal(t, "echo hello", pendingTasks[0].Command)
}

// TestSend_TimesOutWithHeartbeatTimeout - the heartbeat uses its own, shorter
// request timeout, so a stalled control plane fails fast instead of blocking
// for the shared 30s budget.
func TestSend_TimesOutWithHeartbeatTimeout(t *testing.T) {
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-released
	}))
	defer func() {
		close(released)
		server.Close()
	}()

	t.Setenv("SH_CONTROL_PLANE_URL", server.URL)
	t.Setenv("HOSTLINK_API_HEARTBEAT_TIMEOUT", "1s")

	testutil.SetupAgentIdentity(t)

	service, err := New()
	require.NoError(t, err)

	started := time.Now()
	_, err = service.Send()
	elapsed := time.Since(started)

	require.Error(t, err, "expected the heartbeat to fail on the control plane timeout")
	assert.ErrorIs(t, err, context.DeadlineExceeded, "expected a deadline error, got: %v", err)
	assert.Less(t, elapsed, 10*time.Second, "expected the heartbeat to fail fast on its own timeout budget")
}

// noSlowRequests is what a heartbeat carries when nothing was slow.
var noSlowRequests []slowrequest.Count

// TestSend_SendsTheSlowRequestCountsAndEmptiesThem - the heartbeat delivers
// what the reporter counted since the last one, once.
func TestSend_SendsTheSlowRequestCountsAndEmptiesThem(t *testing.T) {
	service, mockSvr, agentstate := setupTestService()
	slow := []slowrequest.Count{{Endpoint: "/api/v1/tasks", Method: "GET", Count: 2, MaxMs: 4100}}
	service.slowCounts.Restore(slow)

	agentstate.On("GetAgentID").Return("agent-123")
	mockSvr.On("Heartbeat", mock.Anything, "agent-123", slow).Return(&apiserver.HeartbeatResponse{}, nil).Once()

	_, err := service.Send()

	require.NoError(t, err)
	mockSvr.AssertExpectations(t)
	assert.Empty(t, service.slowCounts.Drain(), "delivered counts are not sent again")
}

// TestSend_KeepsTheSlowRequestCountsWhenTheHeartbeatFails - a failed heartbeat
// must not lose them; the next one carries them.
func TestSend_KeepsTheSlowRequestCountsWhenTheHeartbeatFails(t *testing.T) {
	service, mockSvr, agentstate := setupTestService()
	slow := []slowrequest.Count{{Endpoint: "/api/v1/agents/:id/heartbeat", Method: "POST", Count: 1, MaxMs: 10012}}
	service.slowCounts.Restore(slow)

	agentstate.On("GetAgentID").Return("agent-123")
	mockSvr.On("Heartbeat", mock.Anything, "agent-123", slow).Return(nil, errors.New("timeout")).Once()

	_, err := service.Send()

	require.Error(t, err)
	assert.Equal(t, slow, service.slowCounts.Drain())
}
