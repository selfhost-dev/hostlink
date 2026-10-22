package pgmonitorrole

import (
	"bytes"
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// RFC 7677's SCRAM-SHA-256 example: password "pencil", salt
// W22ZaJ0SNY7soEsUEjb6gQ==, 4096 iterations. PostgreSQL stores exactly
// StoredKey and ServerKey from it.
func TestVerifier_MatchesTheRFCExample(t *testing.T) {
	salt := []byte{0x5b, 0x6d, 0x99, 0x68, 0x9d, 0x12, 0x35, 0x8e, 0xec, 0xa0, 0x4b, 0x14, 0x12, 0x36, 0xfa, 0x81}

	got := verifier("pencil", salt, 4096)

	assert.Equal(t, "SCRAM-SHA-256$4096:W22ZaJ0SNY7soEsUEjb6gQ==$WG5d8oPm3OtcPnkdi4Uo7BkeZkBFzpcXkuLmtbsT4qY=:wfPLwcE6nTWhTAmQ7tl2KeoiWGPlZqQxSrmfPwDl2dU=", got)
}

func TestEnsureSQL_SendsOnlyTheVerifierAndAMinimalRole(t *testing.T) {
	sql := ensureSQL("selfhost_monitor", "SCRAM-SHA-256$4096:c2FsdA==$a2V5:a2V5")

	assert.Contains(t, sql, "CREATE ROLE selfhost_monitor")
	assert.Contains(t, sql, "NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION")
	assert.Contains(t, sql, "PASSWORD 'SCRAM-SHA-256$4096:c2FsdA==$a2V5:a2V5'")
	assert.Contains(t, sql, "GRANT pg_monitor TO selfhost_monitor")
	assert.Contains(t, sql, `\gexec`, "created only when missing, without a DO block")
}

type fakeExec struct {
	containerID, user string
	cmd               []string
	stdin             []byte
	exitCode          int
	output            string
	err               error
}

func (f *fakeExec) Exec(_ context.Context, containerID, user string, cmd []string, stdin []byte) (int, string, error) {
	f.containerID, f.user, f.cmd, f.stdin = containerID, user, cmd, stdin
	return f.exitCode, f.output, f.err
}

// psql runs inside the container as the postgres OS user, over the local
// socket (trusted by the official image), as the database's superuser, and
// reads the SQL from stdin: the password is never on a command line.
func TestEnsure_RunsPsqlOnTheLocalSocketWithTheSQLOnStdin(t *testing.T) {
	ex := &fakeExec{}
	e := &Ensurer{exec: ex, rand: bytes.NewReader(make([]byte, 16))}

	err := e.Ensure(context.Background(), "cid", "app", "appdb", "selfhost_monitor", "monitor-password-123")

	require.NoError(t, err)
	assert.Equal(t, "cid", ex.containerID)
	assert.Equal(t, "postgres", ex.user)
	assert.Equal(t, []string{"psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-U", "app", "-d", "appdb", "-f", "-"}, ex.cmd)
	assert.NotContains(t, strings.Join(ex.cmd, " "), "monitor-password-123")
	assert.NotContains(t, string(ex.stdin), "monitor-password-123", "only the SCRAM verifier is sent")
	assert.Regexp(t, regexp.MustCompile(`PASSWORD 'SCRAM-SHA-256\$4096:`), string(ex.stdin))
}

func TestEnsure_FailureCarriesARedactedReason(t *testing.T) {
	ex := &fakeExec{exitCode: 2, output: "psql: error: role \"app\" does not exist\nPASSWORD 'SCRAM-SHA-256$4096:abc$def:ghi'"}
	e := &Ensurer{exec: ex, rand: bytes.NewReader(make([]byte, 16))}

	err := e.Ensure(context.Background(), "cid", "app", "appdb", "selfhost_monitor", "pw")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `role "app" does not exist`)
	assert.NotContains(t, err.Error(), "abc$def:ghi")
}

func TestEnsure_ExecErrorIsReturned(t *testing.T) {
	e := &Ensurer{exec: &fakeExec{err: errors.New("docker unavailable")}, rand: bytes.NewReader(make([]byte, 16))}

	assert.ErrorContains(t, e.Ensure(context.Background(), "cid", "app", "appdb", "selfhost_monitor", "pw"), "docker unavailable")
}

func TestEnsure_RefusesAnUnsafeRoleName(t *testing.T) {
	e := &Ensurer{exec: &fakeExec{}, rand: bytes.NewReader(make([]byte, 16))}

	assert.Error(t, e.Ensure(context.Background(), "cid", "app", "appdb", `x; DROP ROLE app`, "pw"))
}
