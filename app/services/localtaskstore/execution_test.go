package localtaskstore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecordReceivedSurvivesRestart(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "task_store.db")
	store := openTestStore(t, storePath, 1024*1024, 1024)

	received, err := store.RecordReceived(TaskReceipt{
		TaskID:             "task-1",
		ExecutionAttemptID: "attempt-1",
	})
	require.NoError(t, err)
	require.Equal(t, TaskStatusReceived, received.Status)
	require.NoError(t, store.Close())

	reopened := openTestStore(t, storePath, 1024*1024, 1024)
	state, err := reopened.TaskState("task-1", "attempt-1")
	require.NoError(t, err)
	require.True(t, state.Exists)
	require.Equal(t, TaskStatusReceived, state.Status)
}

func TestRecordReceivedReturnsExistingDuplicateState(t *testing.T) {
	store := newTestStore(t, 1024*1024, 1024)
	receipt := TaskReceipt{TaskID: "task-1", ExecutionAttemptID: "attempt-1"}

	first, err := store.RecordReceived(receipt)
	require.NoError(t, err)
	second, err := store.RecordReceived(receipt)
	require.NoError(t, err)

	require.True(t, second.Exists)
	require.Equal(t, first.ID, second.ID)
	require.Equal(t, TaskStatusReceived, second.Status)
}

func TestTaskStateTreatsNewAttemptAsDistinct(t *testing.T) {
	store := newTestStore(t, 1024*1024, 1024)

	_, err := store.RecordReceived(TaskReceipt{TaskID: "task-1", ExecutionAttemptID: "attempt-1"})
	require.NoError(t, err)

	state, err := store.TaskState("task-1", "attempt-2")
	require.NoError(t, err)
	require.False(t, state.Exists)
}

func openTestStore(t *testing.T, path string, spoolCapBytes, terminalReserveBytes int64) *Store {
	t.Helper()

	store, err := New(Config{
		Path:                 path,
		SpoolCapBytes:        spoolCapBytes,
		TerminalReserveBytes: terminalReserveBytes,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = store.Close()
	})

	return store
}
