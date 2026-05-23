package localtaskstore

import (
	"errors"
	"hostlink/internal/telemetry/telemetrytest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAppendOutputChunkRotatesOldestChunksAndMarksTruncated(t *testing.T) {
	store := newTestStore(t, 16, 4)

	appendChunk(t, store, "msg-1", "task-1", 1, "12345")
	appendChunk(t, store, "msg-2", "task-1", 2, "67890")
	appendChunk(t, store, "msg-3", "task-1", 3, "abcde")

	messages, err := store.UnackedMessages()
	require.NoError(t, err)
	require.Len(t, messages, 2)
	require.Equal(t, "msg-2", messages[0].MessageID)
	require.Equal(t, "msg-3", messages[1].MessageID)

	state, err := store.TaskState("task-1", "attempt-1")
	require.NoError(t, err)
	require.True(t, state.LocalOutputTruncated)
}

func TestRecordFinalPreservedUnderChunkCapPressure(t *testing.T) {
	store := newTestStore(t, 14, 4)

	appendChunk(t, store, "msg-1", "task-1", 1, "12345")
	appendChunk(t, store, "msg-2", "task-1", 2, "67890")
	require.NoError(t, store.RecordFinal(FinalResult{
		MessageID:          "msg-final-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Status:             "completed",
		Payload:            "done",
	}))

	messages, err := store.UnackedMessages()
	require.NoError(t, err)
	require.Contains(t, messageIDs(messages), "msg-final-1")
}

func TestRecordReceivedFailsWhenTerminalReserveUnavailable(t *testing.T) {
	store := newTestStore(t, 8, 4)
	require.NoError(t, store.RecordFinal(FinalResult{
		MessageID:          "msg-final-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Status:             "completed",
		Payload:            "12345",
	}))

	_, err := store.RecordReceived(TaskReceipt{TaskID: "task-2", ExecutionAttemptID: "attempt-1"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrTerminalReserveUnavailable))
}

func TestAppendOutputChunkRotationEmitsTruncationTelemetry(t *testing.T) {
	telemetryPath := filepath.Join(t.TempDir(), "hostlink-store-telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
	store := newTestStore(t, 16, 4)

	appendChunk(t, store, "msg-1", "task-1", 1, "12345")
	appendChunk(t, store, "msg-2", "task-1", 2, "67890")
	appendChunk(t, store, "msg-3", "task-1", 3, "abcde")

	entries := readTelemetryEntries(t, telemetryPath)
	truncationEvent := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["event"] == "hostlink.local_store.output.rotated"
	})
	truncationMetric := findTelemetryEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.local_store.output.rotated_chunks"
	})
	require.Equal(t, "task-1", truncationEvent["task_id"])
	require.Equal(t, "attempt-1", truncationEvent["execution_attempt_id"])
	require.Equal(t, "task-1", truncationMetric["task_id"])
}

func readTelemetryEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	return telemetrytest.ReadEntries(t, path)
}

func findTelemetryEntry(entries []map[string]any, match func(map[string]any) bool) map[string]any {
	return telemetrytest.FindEntry(entries, match)
}

func messageIDs(messages []OutboxMessage) []string {
	ids := make([]string, 0, len(messages))
	for _, message := range messages {
		ids = append(ids, message.MessageID)
	}
	return ids
}

func appendChunk(t *testing.T, store *Store, messageID, taskID string, sequence int64, payload string) {
	t.Helper()

	require.NoError(t, store.AppendOutputChunk(OutputChunk{
		MessageID:          messageID,
		TaskID:             taskID,
		ExecutionAttemptID: "attempt-1",
		Stream:             "stdout",
		Sequence:           sequence,
		Payload:            payload,
		ByteCount:          int64(len(payload)),
	}))
}
