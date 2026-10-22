// Package pgmonitorrole creates and maintains the agent's own monitoring role
// in a Coolify PostgreSQL container, so the agent never monitors as the
// customer's user: a customer who changes their own password (ALTER USER, which
// the platform cannot see) used to blind monitoring.
//
// The role is created through psql inside the container, as the database's
// bootstrap superuser over the local Unix socket, which the official postgres
// image trusts: no password is needed for that step. The role's password is
// sent as a SCRAM-SHA-256 verifier, never in clear text, so neither the psql
// command line nor a statement log (log_statement) can show it.
package pgmonitorrole

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// Execer runs a command in a container with stdin and returns its exit code
// and combined output.
type Execer interface {
	Exec(ctx context.Context, containerID, user string, cmd []string, stdin []byte) (int, string, error)
}

type Ensurer struct {
	exec Execer
	rand io.Reader
}

func New(exec Execer) *Ensurer {
	return &Ensurer{exec: exec, rand: rand.Reader}
}

// NewDocker uses the Docker API the agent already uses for discovery.
func NewDocker() (*Ensurer, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, err
	}
	return New(&dockerExecer{cli: cli}), nil
}

var safeIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Ensure creates the role if it is missing and (re)sets its password,
// attributes and pg_monitor membership. Idempotent: run again whenever the
// role cannot log in.
func (e *Ensurer) Ensure(ctx context.Context, containerID, superuser, database, role, password string) error {
	if !safeIdentifier.MatchString(role) {
		return fmt.Errorf("unsafe role name %q", role)
	}
	salt := make([]byte, 16)
	if _, err := io.ReadFull(e.rand, salt); err != nil {
		return fmt.Errorf("salt: %w", err)
	}

	cmd := []string{"psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "-U", superuser, "-d", database, "-f", "-"}
	code, output, err := e.exec.Exec(ctx, containerID, "postgres", cmd, []byte(ensureSQL(role, verifier(password, salt, 4096))))
	if err != nil {
		return fmt.Errorf("exec psql: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("psql exited %d: %s", code, redact(output))
	}
	return nil
}

// ensureSQL: created only when missing (\gexec runs the generated statement),
// then forced to a minimal login role every time, so a customer's changes to
// it are undone too. pg_monitor reads every pg_stat_* view and setting the
// collector needs, and nothing else.
//
// lock_timeout / statement_timeout: a customer's open transaction holding the
// role must make this fail fast, never wait. GRANT CONNECT on the database the
// agent monitors: a database hardened with REVOKE CONNECT ... FROM PUBLIC
// would otherwise refuse the role for good.
func ensureSQL(role, verifier string) string {
	return fmt.Sprintf(`SET lock_timeout = '5s';
SET statement_timeout = '15s';
SELECT 'CREATE ROLE %[1]s' WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = '%[1]s') \gexec
ALTER ROLE %[1]s WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT 3 PASSWORD '%[2]s';
GRANT pg_monitor TO %[1]s;
SELECT format('GRANT CONNECT ON DATABASE %%I TO %[1]s', current_database()) \gexec
`, role, verifier)
}

// verifier is PostgreSQL's stored form of a SCRAM-SHA-256 password
// (RFC 5802/7677): SCRAM-SHA-256$<iterations>:<salt>$<StoredKey>:<ServerKey>.
// The platform generates alphanumeric passwords, which SASLprep leaves as
// they are.
func verifier(password string, salt []byte, iterations int) string {
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		panic(err) // only for out-of-range parameters, which are constants here
	}
	clientKey := hmacSHA256(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	serverKey := hmacSHA256(salted, "Server Key")
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, b64(salt), b64(storedKey[:]), b64(serverKey))
}

func hmacSHA256(key []byte, msg string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(msg))
	return m.Sum(nil)
}

var verifierPattern = regexp.MustCompile(`SCRAM-SHA-256\$[^'\s]*`)

// redact keeps psql's reason for the log, never a verifier, and only the
// first lines.
func redact(output string) string {
	output = verifierPattern.ReplaceAllString(output, "SCRAM-SHA-256$[REDACTED]")
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) > 3 {
		lines = lines[:3]
	}
	return strings.Join(lines, " | ")
}

type dockerExecer struct{ cli *client.Client }

func (d *dockerExecer) Exec(ctx context.Context, containerID, user string, cmd []string, stdin []byte) (int, string, error) {
	created, err := d.cli.ContainerExecCreate(ctx, containerID, container.ExecOptions{
		User: user, Cmd: cmd, AttachStdin: true, AttachStdout: true, AttachStderr: true,
	})
	if err != nil {
		return 0, "", err
	}
	attached, err := d.cli.ContainerExecAttach(ctx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return 0, "", err
	}
	defer attached.Close()
	// The hijacked stream does not watch ctx: close it when ctx ends, so a
	// psql that never exits cannot hold the caller.
	stop := context.AfterFunc(ctx, func() { attached.Close() })
	defer stop()

	if _, err := attached.Conn.Write(stdin); err != nil {
		return 0, "", err
	}
	if err := attached.CloseWrite(); err != nil {
		return 0, "", err
	}
	var out bytes.Buffer
	if _, err := stdcopy.StdCopy(&out, &out, attached.Reader); err != nil {
		if ctx.Err() != nil {
			return 0, out.String(), ctx.Err()
		}
		return 0, "", err
	}
	if ctx.Err() != nil {
		return 0, out.String(), ctx.Err()
	}

	inspected, err := d.cli.ContainerExecInspect(ctx, created.ID)
	if err != nil {
		return 0, out.String(), err
	}
	return inspected.ExitCode, out.String(), nil
}
