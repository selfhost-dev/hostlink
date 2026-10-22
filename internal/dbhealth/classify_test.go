package dbhealth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type sysErr struct{ errno syscall.Errno }

func (e *sysErr) Error() string { return e.errno.Error() }
func (e *sysErr) Unwrap() error { return e.errno }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// Only a refusal, a server starting up and a full connection table prove a
// database down. A rejected login, a timeout or a network problem say only
// that the agent could not tell.
func TestClassify(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		kind      string
		down      bool
		connected bool
	}{
		{"refused", fmt.Errorf("ping: %w", &net.OpError{Op: "dial", Err: &sysErr{syscall.ECONNREFUSED}}), "connection_refused", true, false},
		{"refused, mongo topology text", errors.New("server selection error: connection() error occurred during connection handshake: dial tcp 10.0.1.7:27017: connect: connection refused"), "connection_refused", true, false},
		{"deadline", context.DeadlineExceeded, "timeout", false, false},
		{"net timeout", &net.OpError{Op: "dial", Err: timeoutErr{}}, "timeout", false, false},
		{"dns", &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "db"}}, "dns", false, false},
		{"unreachable", &net.OpError{Op: "dial", Err: &sysErr{syscall.EHOSTUNREACH}}, "network", false, false},

		{"mysql access denied", fmt.Errorf("ping: %w", &mysql.MySQLError{Number: 1045, Message: "Access denied for user"}), "auth_failed", false, true},
		{"mysql unknown database", &mysql.MySQLError{Number: 1049}, "database_missing", false, true},
		{"mysql too many connections", &mysql.MySQLError{Number: 1040}, "too_many_connections", true, true},
		{"mysql shutting down", &mysql.MySQLError{Number: 1053}, "starting_up", true, true},
		{"mysql no privilege", &mysql.MySQLError{Number: 1227}, "permission_denied", false, true},
		{"mysql other", &mysql.MySQLError{Number: 1146}, "query_failed", false, true},

		{"mongo auth", mongo.CommandError{Code: 18, Name: "AuthenticationFailed"}, "auth_failed", false, true},
		{"mongo auth text", errors.New("connection() error occurred during connection handshake: auth error: sasl conversation error: unable to authenticate using mechanism \"SCRAM-SHA-256\": (AuthenticationFailed) Authentication failed."), "auth_failed", false, true},
		{"mongo unauthorized", mongo.CommandError{Code: 13, Name: "Unauthorized"}, "permission_denied", false, true},
		{"mongo other", mongo.CommandError{Code: 2, Name: "BadValue"}, "query_failed", false, true},

		{"redis wrongpass", errors.New("WRONGPASS invalid username-password pair or user is disabled."), "auth_failed", false, true},
		{"redis noauth", errors.New("NOAUTH Authentication required."), "auth_failed", false, true},
		{"redis loading", errors.New("LOADING Redis is loading the dataset in memory"), "starting_up", true, true},
		{"redis max clients", errors.New("ERR max number of clients reached"), "too_many_connections", true, true},
		{"redis noperm", errors.New("NOPERM User selfhost_monitor has no permissions to run the 'info' command"), "permission_denied", false, true},

		{"anything else", errors.New("boom"), "connection_failed", false, false},
	}
	for _, c := range cases {
		kind, down, connected := Classify(c.err)
		assert.Equal(t, c.kind, kind, c.name)
		assert.Equal(t, c.down, down, c.name)
		assert.Equal(t, c.connected, connected, c.name)
	}
}

func TestVerdictFromKind(t *testing.T) {
	up, health := Verdict("connection_refused", true, false)
	assert.False(t, *up)
	assert.Equal(t, "unhealthy", health)

	up, health = Verdict("auth_failed", false, true)
	assert.Nil(t, up, "a rejected login is not a verdict on the database")
	assert.Equal(t, "unknown", health)

	up, health = Verdict("permission_denied", false, true)
	assert.True(t, *up, "the server answered: it is up, only the reading failed")
	assert.Equal(t, "healthy", health)
}
