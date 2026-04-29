package localtaskstore

import (
	"hostlink/internal/telemetry/telemetrytest"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecordStartedSurvivesRestart(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "task_store.db")
	store := openTestStore(t, storePath, 1024*1024, 1024)

	_, err := store.RecordReceived(TaskReceipt{TaskID: "task-1", ExecutionAttemptID: "attempt-1"})
	require.NoError(t, err)
	require.NoError(t, store.RecordStarted("task-1", "attempt-1"))
	require.NoError(t, store.Close())

	reopened := openTestStore(t, storePath, 1024*1024, 1024)
	state, err := reopened.TaskState("task-1", "attempt-1")
	require.NoError(t, err)
	require.Equal(t, TaskStatusRunning, state.Status)
}

func TestRecordFinalCreatesUnackedTerminalMessageAcrossRestart(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "task_store.db")
	store := openTestStore(t, storePath, 1024*1024, 1024)

	require.NoError(t, store.RecordFinal(FinalResult{
		MessageID:          "msg-final-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Status:             "completed",
		ExitCode:           0,
		Payload:            `{"status":"completed"}`,
	}))
	require.NoError(t, store.Close())

	reopened := openTestStore(t, storePath, 1024*1024, 1024)
	messages, err := reopened.UnackedMessages()
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, "msg-final-1", messages[0].MessageID)
	require.Equal(t, OutboxMessageTypeFinal, messages[0].Type)
}

func TestAckMessageRemovesOutputChunkFromResendQueue(t *testing.T) {
	store := newTestStore(t, 1024*1024, 1024)

	require.NoError(t, store.AppendOutputChunk(OutputChunk{
		MessageID:          "msg-output-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Stream:             "stdout",
		Sequence:           1,
		Payload:            "hello",
		ByteCount:          5,
	}))
	require.NoError(t, store.AckMessage("msg-output-1"))

	messages, err := store.UnackedMessages()
	require.NoError(t, err)
	require.Empty(t, messages)
}

func TestAckFinalRemovesOutboxButPreservesTaskState(t *testing.T) {
	store := newTestStore(t, 1024*1024, 1024)

	require.NoError(t, store.RecordFinal(FinalResult{
		MessageID:          "msg-final-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Status:             "completed",
		ExitCode:           0,
		Payload:            `{"status":"completed"}`,
	}))
	require.NoError(t, store.AckMessage("msg-final-1"))

	messages, err := store.UnackedMessages()
	require.NoError(t, err)
	require.Empty(t, messages)

	state, err := store.TaskState("task-1", "attempt-1")
	require.NoError(t, err)
	require.True(t, state.Exists)
	require.Equal(t, TaskStatusFinal, state.Status)
}

func TestOutboxMetricsTrackPendingMessagesBytesAndFinals(t *testing.T) {
	telemetryPath := filepath.Join(t.TempDir(), "hostlink-store-telemetry.jsonl")
	t.Setenv("HOSTLINK_WS_TELEMETRY_PATH", telemetryPath)
	store := newTestStore(t, 1024*1024, 1024)

	require.NoError(t, store.AppendOutputChunk(OutputChunk{
		MessageID:          "msg-output-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Stream:             "stdout",
		Sequence:           1,
		Payload:            "hello",
		ByteCount:          5,
	}))
	require.NoError(t, store.RecordFinal(FinalResult{
		MessageID:          "msg-final-1",
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
		Status:             "completed",
		ExitCode:           0,
		Payload:            `{"status":"completed"}`,
	}))

	entries := telemetrytest.ReadEntries(t, telemetryPath)
	pendingMessages := telemetrytest.FindEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.local_store.outbox.pending_messages" && entry["value"] == float64(2)
	})
	pendingBytes := telemetrytest.FindEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.local_store.outbox.pending_bytes" && entry["value"] == float64(27)
	})
	pendingFinals := telemetrytest.FindEntry(entries, func(entry map[string]any) bool {
		return entry["metric_name"] == "hostlink.local_store.outbox.pending_finals" && entry["value"] == float64(1)
	})
	require.Equal(t, "task-1", pendingMessages["task_id"])
	require.Equal(t, "attempt-1", pendingMessages["execution_attempt_id"])
	require.Equal(t, "task-1", pendingBytes["task_id"])
	require.Equal(t, "task-1", pendingFinals["task_id"])
}
