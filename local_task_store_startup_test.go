package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"hostlink/app/services/localtaskstore"
)

func TestRecoverLocalTaskStoreInitializesStoreUnderStatePath(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HOSTLINK_STATE_PATH", stateDir)
	t.Setenv("HOSTLINK_LOCAL_STORE_PATH", "")
	t.Setenv("HOSTLINK_LOCAL_STORE_SPOOL_CAP_BYTES", "1048576")
	t.Setenv("HOSTLINK_LOCAL_STORE_TERMINAL_RESERVE_BYTES", "1024")

	store, err := recoverLocalTaskStore()
	require.NoError(t, err)
	require.NoError(t, store.Close())
	require.FileExists(t, filepath.Join(stateDir, "task_store.db"))
}

func TestRecoverLocalTaskStoreMarksRunningTasksInterrupted(t *testing.T) {
	stateDir := t.TempDir()
	storePath := filepath.Join(stateDir, "task_store.db")
	t.Setenv("HOSTLINK_STATE_PATH", stateDir)
	t.Setenv("HOSTLINK_LOCAL_STORE_PATH", "")
	t.Setenv("HOSTLINK_LOCAL_STORE_SPOOL_CAP_BYTES", "1048576")
	t.Setenv("HOSTLINK_LOCAL_STORE_TERMINAL_RESERVE_BYTES", "1024")

	store, err := localtaskstore.New(localtaskstore.Config{
		Path:                 storePath,
		SpoolCapBytes:        1024 * 1024,
		TerminalReserveBytes: 1024,
	})
	require.NoError(t, err)
	require.NoError(t, store.RecordStarted("task-1", "attempt-1"))
	require.NoError(t, store.Close())

	recovered, err := recoverLocalTaskStore()
	require.NoError(t, err)
	defer recovered.Close()

	state, err := recovered.TaskState("task-1", "attempt-1")
	require.NoError(t, err)
	require.Equal(t, localtaskstore.TaskStatusInterrupted, state.Status)
}
