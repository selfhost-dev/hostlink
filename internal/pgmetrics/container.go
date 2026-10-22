package pgmetrics

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lib/pq"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

// The queries a ContainerCollector runs, one metric (or metric group) each,
// so one failing never blanks the others.
const (
	qHealth = `SELECT 1`
	// Client sessions other than this one: background workers (checkpointer,
	// autovacuum, walwriter…) are not connections, and neither is the agent.
	qConnections = `SELECT count(*)::int FROM pg_stat_activity WHERE backend_type = 'client backend' AND pid <> pg_backend_pid()`
	qMaxConn     = `SELECT current_setting('max_connections')::int`
	// Cumulative since the last stats reset. NULL, not 100, when no block was
	// ever read or hit: no data is not a perfect ratio.
	qCacheHit = `SELECT CASE WHEN COALESCE(sum(blks_hit) + sum(blks_read), 0) = 0 THEN NULL
	                    ELSE round(sum(blks_hit) * 100.0 / (sum(blks_hit) + sum(blks_read)), 2)::float8 END
	             FROM pg_stat_database`
	qStats = `SELECT COALESCE(sum(xact_commit), 0)::bigint, COALESCE(sum(xact_rollback), 0)::bigint,
	                 COALESCE(sum(blks_read), 0)::bigint, min(stats_reset)
	          FROM pg_stat_database`
	qInRecovery  = `SELECT pg_is_in_recovery()`
	qWalReceiver = `SELECT status FROM pg_stat_wal_receiver LIMIT 1`
	qReplayLag   = `SELECT EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp()))::int`
	qStreaming   = `SELECT count(*)::int FROM pg_stat_replication WHERE state = 'streaming'`
	// replay_lag is NULL on a caught-up, idle replica: that is no lag.
	qPrimaryLag = `SELECT COALESCE(MAX(EXTRACT(EPOCH FROM replay_lag)), 0)::int FROM pg_stat_replication WHERE state = 'streaming'`
)

// maxSampleGap: two samples further apart than this are not a rate any more
// (the agent was down, the container paused); the newer one is a new baseline.
const maxSampleGap = 5 * time.Minute

// session is the database connection a collection runs on; a fake in tests.
type session interface {
	QueryRow(ctx context.Context, query string, dest ...any) error
	Timescale(ctx context.Context, m *metrics.PostgreSQLDatabaseMetrics)
	Close() error
}

// Failure is why a collection was not complete, for the agent's own log. The
// payload carries only Kind.
type Failure struct {
	Stage  string // "connect" (no answer to SELECT 1) or "query"
	Metric string // the metric whose query failed, on the query stage
	Kind   string
	Err    error
}

// ContainerCollector collects one Docker-discovered PostgreSQL container
// (a Coolify database). One per container: it owns that database's rate
// baseline, which a single collector shared across databases mixed up.
//
// It reports health, not just numbers (see metrics.PostgreSQLContainerMetrics):
// a refused connection is unhealthy, a timeout or an authentication failure
// is unknown, and a failed query on a database that answered SELECT 1 is a
// healthy database with a partial collection.
type ContainerCollector struct {
	open           func(dsn string) (session, error)
	now            func() time.Time
	timeout        time.Duration
	connectTimeout time.Duration

	last        *PostgresStats
	lastAt      time.Time
	lastSuccess time.Time
}

func NewContainerCollector() *ContainerCollector {
	return &ContainerCollector{
		open:           openSQLSession,
		now:            time.Now,
		timeout:        10 * time.Second,
		connectTimeout: 5 * time.Second,
	}
}

func (c *ContainerCollector) Collect(cred credential.Credential) (m metrics.PostgreSQLContainerMetrics, failure *Failure) {
	start := c.now()
	// Named results: the duration and the last success are stamped on what
	// is returned, on every path.
	defer func() {
		m.CollectionDurationMs = c.now().Sub(start).Milliseconds()
		if !c.lastSuccess.IsZero() {
			at := c.lastSuccess.UTC().Format(time.RFC3339)
			m.LastSuccessAt = &at
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	sess, err := c.open(containerDSN(cred, c.connectTimeout))
	if err == nil {
		defer sess.Close()
		var one int
		err = sess.QueryRow(ctx, qHealth, &one)
	}
	if err != nil {
		kind, down := classify(err)
		if down {
			up := false
			m.Up = &up
			m.Health = metrics.HealthUnhealthy
		} else {
			m.Health = metrics.HealthUnknown
		}
		m.CollectionStatus = metrics.CollectionFailed
		m.CollectionError = kind
		return m, &Failure{Stage: "connect", Kind: kind, Err: err}
	}

	up := true
	m.Up = &up
	m.Health = metrics.HealthHealthy

	var failures []Failure
	run := func(metric, query string, dest ...any) bool {
		if err := sess.QueryRow(ctx, query, dest...); err != nil {
			kind, _ := classify(err)
			if kind == "connection_failed" || kind == "connection_refused" {
				kind = "query_failed"
			}
			failures = append(failures, Failure{Stage: "query", Metric: metric, Kind: kind, Err: err})
			return false
		}
		return true
	}

	var conns, maxConns int
	if run("connections_total", qConnections, &conns) {
		m.ConnectionsTotal = &conns
	}
	if run("max_connections", qMaxConn, &maxConns) {
		m.MaxConnections = &maxConns
	}
	var cache sql.NullFloat64
	if run("cache_hit_ratio", qCacheHit, &cache) && cache.Valid {
		v := cache.Float64
		m.CacheHitRatio = &v
	}
	var st PostgresStats
	var reset sql.NullTime
	if run("transactions_per_second", qStats, &st.XactCommit, &st.XactRollback, &st.BlksRead, &reset) {
		if reset.Valid {
			st.StatsReset = reset.Time
		}
		c.rates(&m, st, c.now())
	}
	c.replication(ctx, sess, &m, run)

	var ts metrics.PostgreSQLDatabaseMetrics
	sess.Timescale(ctx, &ts)
	m.TimescaledbHypertableCount = ts.TimescaledbHypertableCount
	m.TimescaledbChunkCount = ts.TimescaledbChunkCount
	m.TimescaledbCompressedChunkCount = ts.TimescaledbCompressedChunkCount
	m.TimescaledbCompressionRatio = ts.TimescaledbCompressionRatio
	m.TimescaledbTotalSizeBytes = ts.TimescaledbTotalSizeBytes

	const coreQueries = 5 // connections, max, cache, stats, replication
	switch {
	case len(failures) == 0:
		m.CollectionStatus = metrics.CollectionComplete
		c.lastSuccess = c.now()
		return m, nil
	case len(failures) >= coreQueries:
		m.CollectionStatus = metrics.CollectionFailed
	default:
		m.CollectionStatus = metrics.CollectionPartial
	}
	m.CollectionError = failures[0].Kind
	return m, &failures[0]
}

// rates turns two consecutive samples of PostgreSQL's cumulative counters
// into per-second rates. No rate (nil) for the first sample, across a reset
// (stats_reset moved or a counter went backwards), or across a gap longer
// than maxSampleGap; each of those starts a new baseline. A sample that
// failed never reaches here, so the baseline waits for the next good one.
func (c *ContainerCollector) rates(m *metrics.PostgreSQLContainerMetrics, cur PostgresStats, now time.Time) {
	prev, prevAt := c.last, c.lastAt
	c.last, c.lastAt = &cur, now

	if prev == nil || !prev.StatsReset.Equal(cur.StatsReset) ||
		cur.XactCommit < prev.XactCommit || cur.XactRollback < prev.XactRollback || cur.BlksRead < prev.BlksRead {
		return
	}
	gap := now.Sub(prevAt)
	if gap <= 0 || gap > maxSampleGap {
		return
	}

	elapsed := gap.Seconds()
	commits := cur.XactCommit - prev.XactCommit
	tps := float64(commits+cur.XactRollback-prev.XactRollback) / elapsed
	ctps := float64(commits) / elapsed
	brps := float64(cur.BlksRead-prev.BlksRead) / elapsed
	m.TransactionsPerSecond, m.CommittedTxPerSecond, m.BlocksReadPerSecond = &tps, &ctps, &brps
}

// replication: a replica reports its receiver and replay lag; a primary its
// streaming replicas, and a lag only when it has any. A standalone database
// has no replication, so its lag is null, not a reassuring 0.
func (c *ContainerCollector) replication(ctx context.Context, sess session, m *metrics.PostgreSQLContainerMetrics,
	run func(string, string, ...any) bool) {
	var inRecovery bool
	if !run("replication", qInRecovery, &inRecovery) {
		return
	}

	if inRecovery {
		var status sql.NullString
		switch err := sess.QueryRow(ctx, qWalReceiver, &status); {
		case errors.Is(err, sql.ErrNoRows):
			connected := false
			m.ReplicationConnected = &connected
		case err == nil:
			connected := status.Valid && status.String == "streaming"
			m.ReplicationConnected = &connected
		}
		var lag sql.NullInt64
		if run("replication_lag_seconds", qReplayLag, &lag) && lag.Valid {
			v := int(lag.Int64)
			m.ReplicationLagSeconds = &v
		}
		return
	}

	var streaming int
	if !run("active_replica_count", qStreaming, &streaming) {
		return
	}
	m.ActiveReplicaCount = &streaming
	if streaming == 0 {
		return
	}
	var lag sql.NullInt64
	if run("replication_lag_seconds", qPrimaryLag, &lag) && lag.Valid {
		v := int(lag.Int64)
		m.ReplicationLagSeconds = &v
	}
}

// classify maps an error to a category for the payload, and whether it
// proves the database unavailable. Only a refusal, a server starting up or
// shutting down, and a full connection table are proof; a timeout, a network
// or DNS failure and a rejected login are not (the database may be fine and
// the agent unable to see it).
func classify(err error) (kind string, down bool) {
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "28P01", "28000":
			return "auth_failed", false
		case "3D000":
			return "database_missing", false
		case "57P03", "57P01", "57P02":
			return "starting_up", true
		case "53300":
			return "too_many_connections", true
		case "42501":
			return "permission_denied", false
		case "57014":
			return "timeout", false
		}
		if pqErr.Code.Class() == "08" {
			return "connection_failed", false
		}
		return "query_failed", false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "timeout", false
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return "timeout", false
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "connection_refused", true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return "dns", false
	}
	if errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return "network", false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return "connection_failed", false
	}
	return "connection_failed", false
}

// containerDSN quotes every value: a password with a space or a quote broke
// the old unquoted fmt.Sprintf string.
func containerDSN(cred credential.Credential, connectTimeout time.Duration) string {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}
	pairs := [][2]string{
		{"host", cred.Host},
		{"port", strconv.Itoa(cred.Port)},
		{"user", cred.Username},
		{"password", password},
		{"dbname", cred.Database},
		// Coolify databases are created without SSL (selfhost never sets
		// enable_ssl); the agent reaches them on the host's Docker network.
		{"sslmode", "disable"},
		{"connect_timeout", strconv.Itoa(int(connectTimeout.Seconds()))},
		{"application_name", "hostlink-metrics"},
	}
	parts := make([]string, 0, len(pairs))
	for _, kv := range pairs {
		value := strings.ReplaceAll(strings.ReplaceAll(kv[1], `\`, `\\`), `'`, `\'`)
		parts = append(parts, kv[0]+"='"+value+"'")
	}
	return strings.Join(parts, " ")
}

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password\s*=\s*)('(?:[^'\\]|\\.)*'|\S+)`),
	regexp.MustCompile(`(://[^:/@\s]+:)[^@\s]+@`),
}

// SanitizeError is an error's text with anything credential-shaped removed,
// for the agent log. lib/pq does not echo passwords, but a wrapped error or a
// future driver might.
func SanitizeError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	text = credentialPatterns[0].ReplaceAllString(text, "${1}[REDACTED]")
	text = credentialPatterns[1].ReplaceAllString(text, "${1}[REDACTED]@")
	return text
}

type sqlSession struct{ db *sql.DB }

func openSQLSession(dsn string) (session, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return &sqlSession{db: db}, nil
}

func (s *sqlSession) QueryRow(ctx context.Context, query string, dest ...any) error {
	return s.db.QueryRowContext(ctx, query).Scan(dest...)
}

func (s *sqlSession) Timescale(ctx context.Context, m *metrics.PostgreSQLDatabaseMetrics) {
	(&pgmetrics{}).collectTimescaledbMetrics(ctx, s.db, m)
}

func (s *sqlSession) Close() error { return s.db.Close() }
