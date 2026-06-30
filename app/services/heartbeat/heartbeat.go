package heartbeat

import (
	"context"
	"fmt"

	"hostlink/app/services/agentstate"
	"hostlink/config/appconf"
	"hostlink/domain/task"
	"hostlink/internal/apiserver"
)

type Service interface {
	Send() ([]task.Task, error)
}

type heartbeatService struct {
	apiserver  apiserver.HeartbeatOperations
	agentstate agentstate.Operations
}

func New() (*heartbeatService, error) {
	return NewWithConf()
}

func NewWithConf() (*heartbeatService, error) {
	state := agentstate.New(appconf.AgentStatePath())
	if err := state.Load(); err != nil {
		return nil, err
	}

	client, err := apiserver.NewDefaultClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create api client: %w", err)
	}

	return &heartbeatService{
		apiserver:  client,
		agentstate: state,
	}, nil
}

func NewWithDependencies(
	apiserver apiserver.HeartbeatOperations,
	agentstate agentstate.Operations,
) *heartbeatService {
	return &heartbeatService{
		apiserver:  apiserver,
		agentstate: agentstate,
	}
}

func (s *heartbeatService) Send() ([]task.Task, error) {
	agentID := s.agentstate.GetAgentID()
	if agentID == "" {
		return nil, fmt.Errorf("agent not registered: missing agent ID")
	}

	resp, err := s.apiserver.Heartbeat(context.Background(), agentID)
	if err != nil {
		return nil, err
	}
	return resp.PendingTasks, nil
}
