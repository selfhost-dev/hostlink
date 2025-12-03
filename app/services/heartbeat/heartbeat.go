package heartbeat

import (
	"context"
	"fmt"

	"hostlink/app/services/agentstate"
	"hostlink/config/appconf"
	"hostlink/internal/apiserver"
)

type Service interface {
	Send() error
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

func (s *heartbeatService) Send() error {
	agentID := s.agentstate.GetAgentID()
	if agentID == "" {
		return fmt.Errorf("agent not registered: missing agent ID")
	}

	return s.apiserver.Heartbeat(context.Background(), agentID)
}
