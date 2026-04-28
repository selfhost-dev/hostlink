package wsprotocol

import (
	"encoding/json"
	"fmt"
)

const ProtocolVersion = 1

type MessageType string

const (
	TypeAgentHello         MessageType = "agent.hello"
	TypeAgentHelloAck      MessageType = "agent.hello_ack"
	TypeTaskDeliver        MessageType = "task.deliver"
	TypeTaskReceived       MessageType = "task.received"
	TypeTaskStarted        MessageType = "task.started"
	TypeTaskLeaseHeartbeat MessageType = "task.lease_heartbeat"
	TypeTaskOutput         MessageType = "task.output"
	TypeTaskFinal          MessageType = "task.final"
	TypeAck                MessageType = "ack"
	TypeError              MessageType = "error"
)

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type FinalStatus string

const (
	FinalStatusCompleted   FinalStatus = "completed"
	FinalStatusFailed      FinalStatus = "failed"
	FinalStatusInterrupted FinalStatus = "interrupted"
)

type Envelope struct {
	ProtocolVersion    int            `json:"protocol_version"`
	MessageID          string         `json:"message_id"`
	Type               MessageType    `json:"type"`
	AgentID            string         `json:"agent_id"`
	TaskID             string         `json:"task_id,omitempty"`
	ExecutionAttemptID string         `json:"execution_attempt_id,omitempty"`
	Sequence           *int           `json:"sequence,omitempty"`
	SentAt             string         `json:"sent_at"`
	Payload            map[string]any `json:"payload"`
}

type OutputPayload struct {
	Stream           Stream `json:"stream"`
	Data             string `json:"data"`
	ByteCount        int    `json:"byte_count"`
	TruncatedLocally bool   `json:"truncated_locally,omitempty"`
}

type FinalPayload struct {
	Status          FinalStatus `json:"status"`
	ExitCode        int         `json:"exit_code"`
	Output          string      `json:"output,omitempty"`
	Error           string      `json:"error,omitempty"`
	OutputTruncated bool        `json:"output_truncated"`
	ErrorTruncated  bool        `json:"error_truncated"`
}

type TaskDeliverPayload struct {
	Command  string `json:"command"`
	Priority int    `json:"priority"`
}

func (e Envelope) Validate(authenticatedAgentID string) error {
	if e.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("unsupported protocol_version: %d", e.ProtocolVersion)
	}
	if e.MessageID == "" {
		return fmt.Errorf("message_id is required")
	}
	if !IsSupportedType(e.Type) {
		return fmt.Errorf("unsupported type: %s", e.Type)
	}
	if e.AgentID == "" {
		return fmt.Errorf("agent_id is required")
	}
	if e.AgentID != authenticatedAgentID {
		return fmt.Errorf("agent_id does not match authenticated agent")
	}
	if e.SentAt == "" {
		return fmt.Errorf("sent_at is required")
	}
	if e.Payload == nil {
		return fmt.Errorf("payload must be an object")
	}
	if isTaskType(e.Type) && e.TaskID == "" {
		return fmt.Errorf("task_id is required for task messages")
	}
	if isExecutionType(e.Type) && e.ExecutionAttemptID == "" {
		return fmt.Errorf("execution_attempt_id is required for execution messages")
	}
	if e.Type == TypeTaskOutput && e.Sequence == nil {
		return fmt.Errorf("sequence is required for output messages")
	}

	return nil
}

func (p OutputPayload) Validate() error {
	if p.Stream != StreamStdout && p.Stream != StreamStderr {
		return fmt.Errorf("stream must be stdout or stderr")
	}
	if p.ByteCount < 0 {
		return fmt.Errorf("byte_count must be non-negative")
	}

	return nil
}

func (p FinalPayload) Validate() error {
	switch p.Status {
	case FinalStatusCompleted, FinalStatusFailed, FinalStatusInterrupted:
		return nil
	default:
		return fmt.Errorf("status must be completed, failed, or interrupted")
	}
}

func (p TaskDeliverPayload) Validate() error {
	if p.Command == "" {
		return fmt.Errorf("command is required")
	}
	return nil
}

func DecodePayload[T any](e Envelope) (T, error) {
	var payload T

	data, err := json.Marshal(e.Payload)
	if err != nil {
		return payload, fmt.Errorf("marshal payload: %w", err)
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return payload, fmt.Errorf("unmarshal payload: %w", err)
	}

	return payload, nil
}

func IsSupportedType(messageType MessageType) bool {
	switch messageType {
	case TypeAgentHello,
		TypeAgentHelloAck,
		TypeTaskDeliver,
		TypeTaskReceived,
		TypeTaskStarted,
		TypeTaskLeaseHeartbeat,
		TypeTaskOutput,
		TypeTaskFinal,
		TypeAck,
		TypeError:
		return true
	default:
		return false
	}
}

func isTaskType(messageType MessageType) bool {
	return messageType == TypeTaskDeliver ||
		messageType == TypeTaskReceived ||
		messageType == TypeTaskStarted ||
		messageType == TypeTaskLeaseHeartbeat ||
		messageType == TypeTaskOutput ||
		messageType == TypeTaskFinal
}

func isExecutionType(messageType MessageType) bool {
	return isTaskType(messageType)
}
