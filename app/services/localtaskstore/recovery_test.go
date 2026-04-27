package localtaskstore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSnapshotIncludesLocalTruncationState(t *testing.T) {
	store := newTestStore(t, 16, 4)
	appendChunk(t, store, "msg-1", "task-1", 1, "12345")
	appendChunk(t, store, "msg-2", "task-1", 2, "67890")
	appendChunk(t, store, "msg-3", "task-1", 3, "abcde")

	snapshot, err := store.Snapshot()
	require.NoError(t, err)
	require.Len(t, snapshot.Tasks, 1)
	require.Equal(t, "task-1", snapshot.Tasks[0].TaskID)
	require.True(t, snapshot.Tasks[0].LocalOutputTruncated)
}

func TestMarkInterruptedRunningTasksQueuesTerminalRecordAcrossRestart(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "task_store.db")
	store := openTestStore(t, storePath, 1024*1024, 1024)

	require.NoError(t, store.RecordStarted("task-1", "attempt-1"))
	require.NoError(t, store.Close())

	reopened := openTestStore(t, storePath, 1024*1024, 1024)
	require.NoError(t, reopened.MarkInterruptedRunningTasks())

	state, err := reopened.TaskState("task-1", "attempt-1")
	require.NoError(t, err)
	require.Equal(t, TaskStatusInterrupted, state.Status)

	messages, err := reopened.UnackedMessages()
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, OutboxMessageTypeFinal, messages[0].Type)
	require.Contains(t, messages[0].Payload, "interrupted")
}

func TestMarkInterruptedRunningTasksIsIdempotent(t *testing.T) {
	store := newTestStore(t, 1024*1024, 1024)

	require.NoError(t, store.RecordStarted("task-1", "attempt-1"))
	require.NoError(t, store.MarkInterruptedRunningTasks())
	require.NoError(t, store.MarkInterruptedRunningTasks())

	messages, err := store.UnackedMessages()
	require.NoError(t, err)
	require.Len(t, messages, 1)
}
