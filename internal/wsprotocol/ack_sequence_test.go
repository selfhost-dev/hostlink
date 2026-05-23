package wsprotocol

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAcknowledgementPayload(t *testing.T) {
	payload := BuildAck(AckOptions{
		AckedMessageID:        "msg_123",
		AckedType:             TypeTaskOutput,
		TaskID:                "tsk_123",
		ExecutionAttemptID:    "attempt_123",
		HighestOutputSequence: intPtr(12),
	})

	if payload.AckedMessageID != "msg_123" {
		t.Errorf("acked_message_id = %q, want msg_123", payload.AckedMessageID)
	}
	if payload.AckedType != TypeTaskOutput {
		t.Errorf("acked_type = %q, want %q", payload.AckedType, TypeTaskOutput)
	}
	if payload.HighestOutputSequence == nil || *payload.HighestOutputSequence != 12 {
		t.Fatalf("highest_output_sequence = %v, want 12", payload.HighestOutputSequence)
	}
}

func TestSampleAckPayloadUsesCanonicalFieldNames(t *testing.T) {
	payload := BuildAck(AckOptions{
		AckedMessageID:        "msg_123",
		AckedType:             TypeTaskOutput,
		TaskID:                "tsk_123",
		ExecutionAttemptID:    "attempt_123",
		HighestOutputSequence: intPtr(12),
	})

	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal ack: %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}

	if !reflect.DeepEqual(sortedKeys(decoded), []string{"acked_message_id", "acked_type", "execution_attempt_id", "highest_output_sequence", "task_id"}) {
		t.Errorf("ack fields = %v", sortedKeys(decoded))
	}
}

func TestErrorPayload(t *testing.T) {
	payload := BuildError(ErrorOptions{
		Code:                    "output_sequence_gap",
		Message:                 "expected sequence 8",
		Retryable:               true,
		RelatedMessageID:        "msg_123",
		HighestAcceptedSequence: intPtr(7),
	})

	if payload.Code != "output_sequence_gap" {
		t.Errorf("code = %q, want output_sequence_gap", payload.Code)
	}
	if !payload.Retryable {
		t.Error("expected retryable error")
	}
	if payload.RelatedMessageID != "msg_123" {
		t.Errorf("related_message_id = %q, want msg_123", payload.RelatedMessageID)
	}
	if payload.HighestAcceptedSequence == nil || *payload.HighestAcceptedSequence != 7 {
		t.Fatalf("highest_accepted_sequence = %v, want 7", payload.HighestAcceptedSequence)
	}
}

func TestMessageTracker(t *testing.T) {
	tracker := NewMessageTracker()

	if result := tracker.Record("msg_123"); result != MessageAccepted {
		t.Errorf("first record = %v, want %v", result, MessageAccepted)
	}
	if result := tracker.Record("msg_123"); result != MessageDuplicate {
		t.Errorf("second record = %v, want %v", result, MessageDuplicate)
	}
}

func TestOutputSequenceTracker(t *testing.T) {
	t.Run("accepts contiguous sequence", func(t *testing.T) {
		tracker := NewOutputSequenceTracker()

		result := tracker.Record("attempt_123", StreamStdout, 1)

		if result.Status != SequenceAccepted {
			t.Errorf("status = %v, want %v", result.Status, SequenceAccepted)
		}
		if result.HighestOutputSequence != 1 {
			t.Errorf("highest = %d, want 1", result.HighestOutputSequence)
		}
	})

	t.Run("re-acks duplicate sequence", func(t *testing.T) {
		tracker := NewOutputSequenceTracker()
		tracker.Record("attempt_123", StreamStdout, 1)

		result := tracker.Record("attempt_123", StreamStdout, 1)

		if result.Status != SequenceDuplicate {
			t.Errorf("status = %v, want %v", result.Status, SequenceDuplicate)
		}
		if result.HighestOutputSequence != 1 {
			t.Errorf("highest = %d, want 1", result.HighestOutputSequence)
		}
	})

	t.Run("rejects future gaps as retryable", func(t *testing.T) {
		tracker := NewOutputSequenceTracker()
		tracker.Record("attempt_123", StreamStdout, 1)

		result := tracker.Record("attempt_123", StreamStdout, 3)

		if result.Status != SequenceGap {
			t.Errorf("status = %v, want %v", result.Status, SequenceGap)
		}
		if result.HighestOutputSequence != 1 {
			t.Errorf("highest = %d, want 1", result.HighestOutputSequence)
		}
		if result.ExpectedSequence != 2 {
			t.Errorf("expected = %d, want 2", result.ExpectedSequence)
		}
		if !result.Retryable() {
			t.Error("expected gap to be retryable")
		}
	})

	t.Run("tracks streams independently", func(t *testing.T) {
		tracker := NewOutputSequenceTracker()
		tracker.Record("attempt_123", StreamStdout, 1)

		result := tracker.Record("attempt_123", StreamStderr, 1)

		if result.Status != SequenceAccepted {
			t.Errorf("status = %v, want %v", result.Status, SequenceAccepted)
		}
	})
}
