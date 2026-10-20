package heartbeat

import (
	"context"
	"fmt"

	"hostlink/app/services/agentstate"
	"hostlink/config/appconf"
	"hostlink/domain/task"
	"hostlink/internal/apiserver"
	"hostlink/internal/slowrequest"
)

type Service interface {
	Send() ([]task.Task, error)
}

type heartbeatService struct {
	apiserver  apiserver.HeartbeatOperations
	agentstate agentstate.Operations
	// slowCounts is where the agent's slow control-plane requests accumulate
	// between heartbeats; each heartbeat carries and empties them.
	slowCounts *slowrequest.Counts
}

func New() (*heartbeatService, error) {
	return NewWithConf()
}

func NewWithConf() (*heartbeatService, error) {
	state := agentstate.New(appconf.AgentStatePath())
	if err := state.Load(); err != nil {
		return nil, err
	}

	client, err := apiserver.NewHeartbeatClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create api client: %w", err)
	}

	return &heartbeatService{
		apiserver:  client,
		agentstate: state,
		slowCounts: slowrequest.Default,
	}, nil
}

func NewWithDependencies(
	apiserver apiserver.HeartbeatOperations,
	agentstate agentstate.Operations,
) *heartbeatService {
	return &heartbeatService{
		apiserver:  apiserver,
		agentstate: agentstate,
		slowCounts: slowrequest.Default,
	}
}

func (s *heartbeatService) Send() ([]task.Task, error) {
	agentID := s.agentstate.GetAgentID()
	if agentID == "" {
		return nil, fmt.Errorf("agent not registered: missing agent ID")
	}

	slow := s.slowCounts.Drain()
	resp, err := s.apiserver.Heartbeat(context.Background(), agentID, slow)
	if err != nil {
		// Not delivered: the next heartbeat carries them.
		s.slowCounts.Restore(slow)
		return nil, err
	}
	return resp.PendingTasks, nil
}
