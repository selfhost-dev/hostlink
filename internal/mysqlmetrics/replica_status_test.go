package mysqlmetrics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"testing"

	"hostlink/domain/metrics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// go-sql-driver/mysql returns SHOW REPLICA STATUS text columns as []byte, and
// database/sql hands a *interface{} the []byte unchanged. This fake driver
// returns the row the same way, so the collector reads what it reads in
// production (selfhost-dev/selfhost: every MySQL replica reported
// replication_connected=false, because "%v" of []byte("Yes") is "[89 101 115]").
type replicaStatusDriver struct{ values []driver.Value }

func (d replicaStatusDriver) Open(string) (driver.Conn, error) { return replicaStatusConn(d), nil }

type replicaStatusConn replicaStatusDriver

func (c replicaStatusConn) Prepare(string) (driver.Stmt, error) { return replicaStatusStmt(c), nil }
func (c replicaStatusConn) Close() error                        { return nil }
func (c replicaStatusConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

type replicaStatusStmt replicaStatusDriver

func (s replicaStatusStmt) Close() error                               { return nil }
func (s replicaStatusStmt) NumInput() int                              { return 0 }
func (s replicaStatusStmt) Exec([]driver.Value) (driver.Result, error) { return nil, driver.ErrSkip }
func (s replicaStatusStmt) Query([]driver.Value) (driver.Rows, error) {
	return &replicaStatusRows{values: s.values}, nil
}

type replicaStatusRows struct {
	values []driver.Value
	done   bool
}

func (r *replicaStatusRows) Columns() []string {
	return []string{"Replica_IO_State", "Replica_IO_Running", "Replica_SQL_Running", "Seconds_Behind_Source"}
}
func (r *replicaStatusRows) Close() error { return nil }
func (r *replicaStatusRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	copy(dest, r.values)
	return nil
}

func TestCollectReplication_ReadsTheDriversByteValues(t *testing.T) {
	sql.Register("replica-status-running", replicaStatusDriver{values: []driver.Value{
		[]byte("Waiting for source to send event"), []byte("Yes"), []byte("Yes"), []byte("7"),
	}})
	db, err := sql.Open("replica-status-running", "")
	require.NoError(t, err)
	defer db.Close()

	var m metrics.MySQLDatabaseMetrics
	require.NoError(t, (&mysqlCollector{}).collectReplication(context.Background(), db, &m))

	require.NotNil(t, m.ReplicationConnected)
	assert.True(t, *m.ReplicationConnected, "Replica_IO_Running=Yes is a connected link")
	require.NotNil(t, m.ReplicationLagSeconds)
	assert.Equal(t, 7, *m.ReplicationLagSeconds)
}

func TestCollectReplication_StoppedIOThreadIsNotConnected(t *testing.T) {
	sql.Register("replica-status-stopped", replicaStatusDriver{values: []driver.Value{
		[]byte(""), []byte("No"), []byte("Yes"), nil,
	}})
	db, err := sql.Open("replica-status-stopped", "")
	require.NoError(t, err)
	defer db.Close()

	var m metrics.MySQLDatabaseMetrics
	require.NoError(t, (&mysqlCollector{}).collectReplication(context.Background(), db, &m))

	require.NotNil(t, m.ReplicationConnected)
	assert.False(t, *m.ReplicationConnected)
	assert.Nil(t, m.ReplicationLagSeconds, "Seconds_Behind_Source is NULL while a thread is stopped: no lag, not 0")
}

func TestCollectReplication_ConnectingIsNotConnected(t *testing.T) {
	sql.Register("replica-status-connecting", replicaStatusDriver{values: []driver.Value{
		[]byte("Connecting to source"), []byte("Connecting"), []byte("Yes"), nil,
	}})
	db, err := sql.Open("replica-status-connecting", "")
	require.NoError(t, err)
	defer db.Close()

	var m metrics.MySQLDatabaseMetrics
	require.NoError(t, (&mysqlCollector{}).collectReplication(context.Background(), db, &m))

	require.NotNil(t, m.ReplicationConnected)
	assert.False(t, *m.ReplicationConnected, "the IO thread is retrying, the link is not up")
}
