package localtaskstore

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCreatesStoreFileUnderConfiguredPath(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "nested", "task_store.db")

	store, err := New(Config{
		Path:                 storePath,
		SpoolCapBytes:        1024 * 1024,
		TerminalReserveBytes: 1024,
	})
	require.NoError(t, err)
	require.NoError(t, store.Close())
	require.FileExists(t, storePath)
}

func TestNewMigratesExecutionAndOutboxTables(t *testing.T) {
	store := newTestStore(t, 1024*1024, 1024)

	require.True(t, store.db.Migrator().HasTable(&taskExecutionRecord{}))
	require.True(t, store.db.Migrator().HasTable(&outboxMessageRecord{}))
}

func TestNewReopensExistingStore(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "task_store.db")

	store, err := New(Config{
		Path:                 storePath,
		SpoolCapBytes:        1024 * 1024,
		TerminalReserveBytes: 1024,
	})
	require.NoError(t, err)
	require.NoError(t, store.Close())

	reopened, err := New(Config{
		Path:                 storePath,
		SpoolCapBytes:        1024 * 1024,
		TerminalReserveBytes: 1024,
	})
	require.NoError(t, err)
	require.NoError(t, reopened.Close())
}

func TestNewDefaultUsesConfiguredAppconfValues(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("HOSTLINK_STATE_PATH", stateDir)
	t.Setenv("HOSTLINK_LOCAL_STORE_PATH", "")
	t.Setenv("HOSTLINK_LOCAL_STORE_SPOOL_CAP_BYTES", "1048576")
	t.Setenv("HOSTLINK_LOCAL_STORE_TERMINAL_RESERVE_BYTES", "1024")

	store, err := NewDefault()
	require.NoError(t, err)
	require.NoError(t, store.Close())
	require.FileExists(t, filepath.Join(stateDir, "task_store.db"))
}

func newTestStore(t *testing.T, spoolCapBytes, terminalReserveBytes int64) *Store {
	t.Helper()

	store, err := New(Config{
		Path:                 filepath.Join(t.TempDir(), "task_store.db"),
		SpoolCapBytes:        spoolCapBytes,
		TerminalReserveBytes: terminalReserveBytes,
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, store.Close())
	})

	return store
}
