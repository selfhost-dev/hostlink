package wsprotocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func validOutputEnvelope() Envelope {
	return Envelope{
		ProtocolVersion:    ProtocolVersion,
		MessageID:          "msg_123",
		Type:               TypeTaskOutput,
		AgentID:            "agt_123",
		TaskID:             "tsk_123",
		ExecutionAttemptID: "attempt_123",
		Sequence:           intPtr(1),
		SentAt:             "2026-04-27T00:00:00Z",
		Payload: map[string]any{
			"stream":     "stdout",
			"data":       "hello\n",
			"byte_count": float64(6),
		},
	}
}

func TestEnvelopeJSONRoundTrip(t *testing.T) {
	original := validOutputEnvelope()

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	var decoded Envelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	if decoded.ProtocolVersion != ProtocolVersion {
		t.Errorf("protocol_version = %d, want %d", decoded.ProtocolVersion, ProtocolVersion)
	}
	if decoded.MessageID != original.MessageID {
		t.Errorf("message_id = %q, want %q", decoded.MessageID, original.MessageID)
	}
	if decoded.Type != TypeTaskOutput {
		t.Errorf("type = %q, want %q", decoded.Type, TypeTaskOutput)
	}
	if decoded.Sequence == nil || *decoded.Sequence != 1 {
		t.Fatalf("sequence = %v, want 1", decoded.Sequence)
	}

	payload, err := DecodePayload[OutputPayload](decoded)
	if err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Stream != StreamStdout {
		t.Errorf("stream = %q, want %q", payload.Stream, StreamStdout)
	}
}

func TestSampleOutputEnvelopeUsesCanonicalFieldNames(t *testing.T) {
	data, err := json.Marshal(validOutputEnvelope())
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	for _, field := range []string{"protocol_version", "message_id", "type", "agent_id", "task_id", "execution_attempt_id", "sequence", "sent_at", "payload"} {
		if _, ok := decoded[field]; !ok {
			t.Errorf("expected field %q", field)
		}
	}

	payload, ok := decoded["payload"].(map[string]any)
	if !ok {
		t.Fatalf("payload is %T, want object", decoded["payload"])
	}
	if !reflect.DeepEqual(sortedKeys(payload), []string{"byte_count", "data", "stream"}) {
		t.Errorf("payload fields = %v", sortedKeys(payload))
	}
}

func TestEnvelopeValidate(t *testing.T) {
	t.Run("accepts complete task output envelope", func(t *testing.T) {
		env := validOutputEnvelope()

		if err := env.Validate("agt_123"); err != nil {
			t.Fatalf("expected envelope to validate, got %v", err)
		}
	})

	t.Run("rejects unsupported protocol version", func(t *testing.T) {
		env := validOutputEnvelope()
		env.ProtocolVersion = 2

		if err := env.Validate("agt_123"); err == nil {
			t.Fatal("expected unsupported protocol version error")
		}
	})

	t.Run("rejects mismatched agent", func(t *testing.T) {
		env := validOutputEnvelope()

		if err := env.Validate("agt_other"); err == nil {
			t.Fatal("expected mismatched agent error")
		}
	})

	t.Run("rejects task messages without task ID", func(t *testing.T) {
		env := validOutputEnvelope()
		env.TaskID = ""

		if err := env.Validate("agt_123"); err == nil {
			t.Fatal("expected missing task ID error")
		}
	})

	t.Run("accepts empty payload for non-task message", func(t *testing.T) {
		env := validOutputEnvelope()
		env.Type = TypeAgentHello
		env.TaskID = ""
		env.ExecutionAttemptID = ""
		env.Sequence = nil
		env.Payload = map[string]any{}

		if err := env.Validate("agt_123"); err != nil {
			t.Fatalf("expected empty payload to validate, got %v", err)
		}
	})
}

func TestAcceptedMessageTypes(t *testing.T) {
	expected := []MessageType{
		TypeAgentHello,
		TypeAgentHelloAck,
		TypeTaskDeliver,
		TypeTaskReceived,
		TypeTaskStarted,
		TypeTaskLeaseHeartbeat,
		TypeTaskOutput,
		TypeTaskFinal,
		TypeAck,
		TypeError,
	}

	for _, messageType := range expected {
		if !IsSupportedType(messageType) {
			t.Errorf("expected %q to be supported", messageType)
		}
	}
}

func TestPayloadValidation(t *testing.T) {
	t.Run("valid output payload", func(t *testing.T) {
		payload := OutputPayload{Stream: StreamStderr, Data: "oops", ByteCount: 4}

		if err := payload.Validate(); err != nil {
			t.Fatalf("expected output payload to validate, got %v", err)
		}
	})

	t.Run("invalid output stream", func(t *testing.T) {
		payload := OutputPayload{Stream: "combined", Data: "oops", ByteCount: 4}

		if err := payload.Validate(); err == nil {
			t.Fatal("expected invalid stream error")
		}
	})

	t.Run("valid final payload", func(t *testing.T) {
		payload := FinalPayload{Status: FinalStatusCompleted, ExitCode: 0}

		if err := payload.Validate(); err != nil {
			t.Fatalf("expected final payload to validate, got %v", err)
		}
	})

	t.Run("invalid final status", func(t *testing.T) {
		payload := FinalPayload{Status: "timed_out", ExitCode: 1}

		if err := payload.Validate(); err == nil {
			t.Fatal("expected invalid status error")
		}
	})
}

func TestHelloPayloadUsesReconnectSnapshotShape(t *testing.T) {
	payload := HelloPayload{
		RunningTask: &RunningTaskSnapshot{
			TaskID:             "task-1",
			ExecutionAttemptID: "attempt-1",
			StartedAt:          "2026-04-28T00:00:00Z",
			LastOutputSequence: map[string]int{"stdout": 2, "stderr": 1},
		},
		ReceivedNotStarted: []ReceivedNotStartedAttempt{{TaskID: "task-2", ExecutionAttemptID: "attempt-2", ReceivedAt: "2026-04-28T00:00:01Z"}},
		UnackedFinals: []UnackedFinalSnapshot{{
			MessageID:          "msg-final-1",
			TaskID:             "task-3",
			ExecutionAttemptID: "attempt-3",
			Status:             FinalStatusCompleted,
		}},
		UnackedOutput: []UnackedOutputRange{{TaskID: "task-1", ExecutionAttemptID: "attempt-1", Stream: StreamStdout, FirstSequence: 2, LastSequence: 4}},
		SpoolStatus:   SpoolStatus{BytesUsed: 128, ByteCap: 1024, HasRotatedChunks: true},
		ClientVersion: "test-version",
	}

	if err := payload.Validate(); err != nil {
		t.Fatalf("expected hello payload to validate, got %v", err)
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal hello payload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal hello payload: %v", err)
	}
	if !reflect.DeepEqual(sortedKeys(decoded), []string{"client_version", "received_not_started", "running_task", "spool_status", "unacked_finals", "unacked_output"}) {
		t.Fatalf("hello payload fields = %v", sortedKeys(decoded))
	}
	lastOutput, ok := decoded["running_task"].(map[string]any)["last_output_sequence"].(map[string]any)
	if !ok || lastOutput["stdout"] != float64(2) || lastOutput["stderr"] != float64(1) {
		t.Fatalf("last_output_sequence = %#v", decoded["running_task"])
	}
}

func TestHelloAckPayloadUsesReconciliationDirectiveShape(t *testing.T) {
	payload := HelloAckPayload{
		AckedMessageID:              "msg-hello",
		AckedType:                   TypeAgentHello,
		AcknowledgedFinalMessageIDs: []string{"msg-final-1"},
		DiscardedAttempts:           []DiscardedAttempt{{TaskID: "task-2", ExecutionAttemptID: "attempt-2", Reason: "stale_attempt"}},
		OutputReplay:                []OutputReplayDirective{{TaskID: "task-1", ExecutionAttemptID: "attempt-1", Stream: StreamStderr, NextSequence: 14}},
	}

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal hello ack: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal hello ack: %v", err)
	}
	if !reflect.DeepEqual(sortedKeys(decoded), []string{"acked_message_id", "acked_type", "acknowledged_final_message_ids", "discarded_attempts", "output_replay"}) {
		t.Fatalf("hello ack fields = %v", sortedKeys(decoded))
	}
}

func intPtr(value int) *int {
	return &value
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
