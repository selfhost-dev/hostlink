//go:build integration

package pgmetrics

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/user"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

// Runs ContainerCollector against a real PostgreSQL: PGHOST / PGPORT / PGUSER
// (defaults localhost, 5432, the OS user), a role that may create databases
// and roles. It creates a scratch database and two roles and drops them after.
// Skipped when no server answers.

const (
	itDatabase = "hostlink_pgmetrics_it"
	itReader   = "hostlink_pgmetrics_it_reader"
	// A password with a space and a quote: the DSN must quote it.
	itPassword = `it pass'word`
)

func itAdmin(t *testing.T) (credential.Credential, *sql.DB) {
	t.Helper()
	host := envOr("PGHOST", "localhost")
	port, _ := strconv.Atoi(envOr("PGPORT", "5432"))
	name := os.Getenv("PGUSER")
	if name == "" {
		if u, err := user.Current(); err == nil {
			name = u.Username
		}
	}
	admin := credential.Credential{Host: host, Port: port, Username: name, Database: "postgres"}
	db, err := sql.Open("postgres", containerDSN(admin, 3*time.Second))
	require.NoError(t, err)
	if err := db.Ping(); err != nil {
		t.Skipf("no PostgreSQL at %s:%d: %v", host, port, err)
	}
	return admin, db
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func itSetup(t *testing.T) (admin credential.Credential, reader credential.Credential) {
	t.Helper()
	admin, db := itAdmin(t)
	exec := func(db *sql.DB, q string) {
		t.Helper()
		_, err := db.Exec(q)
		require.NoError(t, err, q)
	}
	drop := func() {
		_, _ = db.Exec(fmt.Sprintf(`DROP DATABASE IF EXISTS %s WITH (FORCE)`, itDatabase))
		_, _ = db.Exec(fmt.Sprintf(`DROP ROLE IF EXISTS %s`, itReader))
	}
	drop()
	exec(db, fmt.Sprintf(`CREATE ROLE %s LOGIN PASSWORD 'it pass''word'`, itReader))
	exec(db, fmt.Sprintf(`CREATE DATABASE %s`, itDatabase))
	t.Cleanup(func() { drop(); db.Close() })

	pw := itPassword
	reader = credential.Credential{Host: admin.Host, Port: admin.Port, Username: itReader, Password: &pw, Database: itDatabase}
	return admin, reader
}

// 1 + 6 + 8 + 9: a reachable standalone server; every metric collected, a
// real rate on the second sample, no replication.
func TestIntegration_ReachableStandaloneServer(t *testing.T) {
	_, reader := itSetup(t)
	cc := NewContainerCollector()

	first, failure := cc.Collect(reader)
	require.Nil(t, failure, "%+v", failure)
	assert.Equal(t, metrics.HealthHealthy, first.Health)
	assert.True(t, *first.Up)
	assert.Equal(t, metrics.CollectionComplete, first.CollectionStatus)
	require.NotNil(t, first.ConnectionsTotal)
	assert.GreaterOrEqual(t, *first.ConnectionsTotal, 0, "the agent's own session is not counted")
	assert.Greater(t, *first.MaxConnections, 0)
	assert.Nil(t, first.TransactionsPerSecond, "one sample is not a rate")
	assert.Nil(t, first.ReplicationLagSeconds, "no replication: null, not 0")
	assert.Equal(t, 0, *first.ActiveReplicaCount)
	require.NotNil(t, first.LastSuccessAt)

	time.Sleep(1100 * time.Millisecond)
	second, failure := cc.Collect(reader)
	require.Nil(t, failure)
	require.NotNil(t, second.TransactionsPerSecond)
	assert.GreaterOrEqual(t, *second.TransactionsPerSecond, 0.0)
	require.NotNil(t, second.BlocksReadPerSecond)
}

// 2: nothing listens on the address.
func TestIntegration_NothingListeningIsUnhealthy(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()

	m, failure := NewContainerCollector().Collect(credential.Credential{Host: "127.0.0.1", Port: port, Username: "x", Database: "x"})

	assert.Equal(t, metrics.HealthUnhealthy, m.Health)
	assert.False(t, *m.Up)
	assert.Equal(t, "connection_refused", m.CollectionError)
	assert.Equal(t, "connect", failure.Stage)
	assert.Nil(t, m.ConnectionsTotal)
}

// 3: a login the server rejects. It answered, so it is not down.
func TestIntegration_RejectedLoginIsUnknown(t *testing.T) {
	admin, _ := itSetup(t)

	m, _ := NewContainerCollector().Collect(credential.Credential{Host: admin.Host, Port: admin.Port, Username: "no_such_role_hostlink", Database: itDatabase})

	assert.Equal(t, metrics.HealthUnknown, m.Health)
	assert.Nil(t, m.Up)
	assert.Equal(t, "auth_failed", m.CollectionError)
}

func TestIntegration_MissingDatabaseIsUnknown(t *testing.T) {
	_, reader := itSetup(t)
	reader.Database = "no_such_database_hostlink"

	m, _ := NewContainerCollector().Collect(reader)

	assert.Equal(t, metrics.HealthUnknown, m.Health)
	assert.Equal(t, "database_missing", m.CollectionError)
}

// 4 + 5: SELECT 1 answers, the statistics views are not readable.
func TestIntegration_NoPermissionOnStatisticsIsPartial(t *testing.T) {
	admin, reader := itSetup(t)
	admin.Database = itDatabase
	db, err := sql.Open("postgres", containerDSN(admin, 3*time.Second))
	require.NoError(t, err)
	defer db.Close()
	// pg_catalog views are per database: this touches the scratch database only.
	_, err = db.Exec(`REVOKE SELECT ON pg_catalog.pg_stat_database FROM PUBLIC`)
	require.NoError(t, err)

	m, failure := NewContainerCollector().Collect(reader)

	assert.Equal(t, metrics.HealthHealthy, m.Health)
	assert.True(t, *m.Up)
	assert.Equal(t, metrics.CollectionPartial, m.CollectionStatus)
	assert.Equal(t, "permission_denied", m.CollectionError)
	assert.Equal(t, "query", failure.Stage)
	assert.Nil(t, m.CacheHitRatio)
	assert.NotNil(t, m.ConnectionsTotal, "the readable metrics are still reported")
	assert.NotNil(t, m.MaxConnections)
}

// 10: the server is up; the agent's own deadline runs out before it can
// tell. Unknown, never down.
func TestIntegration_MonitoringTimeoutIsUnknownNotDown(t *testing.T) {
	_, reader := itSetup(t)
	cc := NewContainerCollector()
	cc.timeout = time.Nanosecond

	m, _ := cc.Collect(reader)

	assert.Equal(t, metrics.HealthUnknown, m.Health)
	assert.Nil(t, m.Up)
	assert.Equal(t, "timeout", m.CollectionError)
}
