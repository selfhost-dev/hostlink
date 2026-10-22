// Package dbhealth turns a database driver's error into the health verdict a
// Docker-discovered (Coolify) database's metric set carries, for MySQL,
// MongoDB and Redis (PostgreSQL has its own, in pgmetrics):
//
//   - healthy: the server answered (a failed reading after that is a
//     partial or failed collection, not a down database);
//   - unhealthy: confirmed unavailable (refused, starting up or shutting
//     down, connection table full);
//   - unknown: the agent could not tell (timeout, network, DNS, a rejected
//     login, a missing database). Never reported as down.
package dbhealth

import (
	"context"
	"errors"
	"net"
	"strings"
	"syscall"

	"github.com/go-sql-driver/mysql"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const (
	Healthy   = "healthy"
	Unhealthy = "unhealthy"
	Unknown   = "unknown"
)

// Classify returns the failure category, whether it proves the database
// down, and whether the server answered at all (so the failure is in the
// reading, not the connection).
func Classify(err error) (kind string, down, connected bool) {
	var my *mysql.MySQLError
	if errors.As(err, &my) {
		switch my.Number {
		case 1045, 1698, 1251:
			return "auth_failed", false, true
		case 1049:
			return "database_missing", false, true
		case 1130:
			// No account admits the agent's host: the server answered but
			// turns every login from here away, root included.
			return "host_not_allowed", false, true
		case 1129:
			return "host_blocked", false, true
		case 1040, 1203:
			return "too_many_connections", true, true
		case 1053, 1077:
			return "starting_up", true, true
		case 1044, 1142, 1227, 1370:
			return "permission_denied", false, true
		}
		return "query_failed", false, true
	}

	var cmd mongo.CommandError
	if errors.As(err, &cmd) {
		switch cmd.Code {
		case 18:
			return "auth_failed", false, true
		case 13:
			return "permission_denied", false, true
		case 91, 189:
			return "starting_up", true, true
		}
		return "query_failed", false, true
	}

	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout", false, false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", false, false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection_refused", true, false
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns", false, false
	}
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return "network", false, false
	}

	// Redis replies and the MongoDB driver's topology errors arrive as text.
	text := err.Error()
	switch {
	case strings.HasPrefix(text, "WRONGPASS"), strings.HasPrefix(text, "NOAUTH"),
		strings.Contains(text, "AuthenticationFailed"), strings.Contains(text, "auth error"):
		return "auth_failed", false, true
	case strings.HasPrefix(text, "NOPERM"):
		return "permission_denied", false, true
	case strings.HasPrefix(text, "LOADING"):
		return "starting_up", true, true
	case strings.Contains(text, "max number of clients"):
		return "too_many_connections", true, true
	case strings.Contains(text, "connection refused"):
		return "connection_refused", true, false
	case strings.Contains(text, "i/o timeout"), strings.Contains(text, "context deadline exceeded"):
		return "timeout", false, false
	case strings.Contains(text, "no such host"):
		return "dns", false, false
	}
	return "connection_failed", false, false
}

// Verdict: up and health for a failure. A server that answered and let the
// agent in is up (only the reading failed); a proven outage is down;
// anything else is unknown, with up null, including a server that answered
// but turned the login away before any reading (refusedLogin).
func Verdict(kind string, down, connected bool) (*bool, string) {
	switch {
	case down:
		up := false
		return &up, Unhealthy
	case connected && !refusedLogin[kind]:
		up := true
		return &up, Healthy
	default:
		return nil, Unknown
	}
}

var refusedLogin = map[string]bool{
	"auth_failed": true, "database_missing": true, "host_not_allowed": true, "host_blocked": true,
}
