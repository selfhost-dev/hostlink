//go:build integration

package pgmonitorrole

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
	"hostlink/internal/pgmetrics"
)

// Runs the real ensure SQL with the local psql (standing in for docker exec
// into the container) against a real PostgreSQL, then logs in as the role it
// made, with the password it was given, and collects. PGHOST / PGPORT /
// PGUSER as for the pgmetrics integration tests; skipped when no server answers.
// PGMONITOR_IT_LOGIN_HOST is where the role logs in (default PGHOST): point
// PGHOST at a trusted local socket and this at a TCP address whose pg_hba
// line demands scram-sha-256, and the login proves the stored verifier.

const (
	itDB   = "hostlink_pgmonitor_it"
	itRole = "hostlink_pgmonitor_it_monitor"
)

type localPsql struct{}

func (localPsql) Exec(ctx context.Context, _, _ string, cmd []string, stdin []byte) (int, string, error) {
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...)
	c.Stdin = bytes.NewReader(stdin)
	out, err := c.CombinedOutput()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode(), string(out), nil
	}
	return 0, string(out), err
}

func setup(t *testing.T) (host string, port int, admin string, db *sql.DB) {
	t.Helper()
	adminHost := envOr("PGHOST", "localhost")
	host = envOr("PGMONITOR_IT_LOGIN_HOST", adminHost)
	port, _ = strconv.Atoi(envOr("PGPORT", "5432"))
	admin = os.Getenv("PGUSER")
	if admin == "" {
		u, _ := user.Current()
		admin = u.Username
	}
	db, err := sql.Open("postgres", fmt.Sprintf("host=%s port=%d user=%s dbname=postgres sslmode=disable", adminHost, port, admin))
	require.NoError(t, err)
	if err := db.Ping(); err != nil {
		t.Skipf("no PostgreSQL: %v", err)
	}
	drop := func() {
		_, _ = db.Exec(`DROP DATABASE IF EXISTS ` + itDB + ` WITH (FORCE)`)
		_, _ = db.Exec(`DROP ROLE IF EXISTS ` + itRole)
	}
	drop()
	_, err = db.Exec(`CREATE DATABASE ` + itDB)
	require.NoError(t, err)
	t.Cleanup(func() { drop(); db.Close() })
	return host, port, admin, db
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func TestIntegration_TheRoleLogsInWithItsPasswordAndReadsEveryMetric(t *testing.T) {
	host, port, admin, db := setup(t)
	password := "Mon1torPasswordFromSelfhost0123456789ab"

	require.NoError(t, New(localPsql{}).Ensure(context.Background(), "local", admin, itDB, itRole, password))

	var super, login, monitor bool
	require.NoError(t, db.QueryRow(`SELECT rolsuper, rolcanlogin, pg_has_role(oid, 'pg_monitor', 'MEMBER') FROM pg_roles WHERE rolname = $1`, itRole).Scan(&super, &login, &monitor))
	assert.False(t, super, "never a superuser")
	assert.True(t, login)
	assert.True(t, monitor)

	pw := password
	m, failure := pgmetrics.NewContainerCollector().Collect(credential.Credential{Host: host, Port: port, Username: itRole, Password: &pw, Database: itDB})
	require.Nil(t, failure, "%+v", failure)
	assert.Equal(t, metrics.HealthHealthy, m.Health)
	assert.Equal(t, metrics.CollectionComplete, m.CollectionStatus)
	assert.NotNil(t, m.CacheHitRatio)
}

// A customer who changes the role's password (or anything else about it) is
// undone by the next ensure, which runs whenever the role cannot log in.
func TestIntegration_EnsureIsIdempotentAndRestoresATamperedRole(t *testing.T) {
	host, port, admin, db := setup(t)
	password := "SecondMonitorPassword0123456789abcdefgh"
	e := New(localPsql{})
	require.NoError(t, e.Ensure(context.Background(), "local", admin, itDB, itRole, password))

	_, err := db.Exec(`ALTER ROLE ` + itRole + ` PASSWORD 'customer-changed-it' NOLOGIN`)
	require.NoError(t, err)
	pw := password
	cred := credential.Credential{Host: host, Port: port, Username: itRole, Password: &pw, Database: itDB}
	broken, _ := pgmetrics.NewContainerCollector().Collect(cred)
	assert.Equal(t, "auth_failed", broken.CollectionError)

	require.NoError(t, e.Ensure(context.Background(), "local", admin, itDB, itRole, password))
	fixed, failure := pgmetrics.NewContainerCollector().Collect(cred)
	require.Nil(t, failure, "%+v", failure)
	assert.Equal(t, metrics.HealthHealthy, fixed.Health)
}
