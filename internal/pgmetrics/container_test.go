package pgmetrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

// fakeSession answers each query from a table; a query with no row errors,
// so a test only lists what the database it describes would answer.
type fakeSession struct {
	rows   map[string][]any
	errs   map[string]error
	closed bool
}

func (f *fakeSession) QueryRow(_ context.Context, query string, dest ...any) error {
	if err, ok := f.errs[query]; ok {
		return err
	}
	vals, ok := f.rows[query]
	if !ok {
		return fmt.Errorf("unexpected query %q", query)
	}
	for i, d := range dest {
		switch p := d.(type) {
		case *int:
			*p = vals[i].(int)
		case *int64:
			*p = vals[i].(int64)
		case *bool:
			*p = vals[i].(bool)
		case *sql.NullFloat64:
			*p = vals[i].(sql.NullFloat64)
		case *sql.NullInt64:
			*p = vals[i].(sql.NullInt64)
		case *sql.NullString:
			*p = vals[i].(sql.NullString)
		case *sql.NullTime:
			*p = vals[i].(sql.NullTime)
		default:
			return fmt.Errorf("fake cannot scan into %T", d)
		}
	}
	return nil
}

func (f *fakeSession) Timescale(context.Context, *metrics.PostgreSQLDatabaseMetrics) {}
func (f *fakeSession) Close() error                                                  { f.closed = true; return nil }

var statsReset = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

// A healthy standalone primary: 3 client connections, 99.5% cache hits,
// counters at `commits`, no replicas.
func healthyDB(commits int64) *fakeSession {
	return &fakeSession{rows: map[string][]any{
		qHealth:      {1},
		qConnections: {3},
		qMaxConn:     {100},
		qCacheHit:    {sql.NullFloat64{Float64: 99.5, Valid: true}},
		qStats:       {commits, int64(10), int64(500), sql.NullTime{Time: statsReset, Valid: true}},
		qInRecovery:  {false},
		qStreaming:   {0},
	}}
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func collectorFor(sess *fakeSession, openErr error, c *clock) *ContainerCollector {
	cc := NewContainerCollector()
	cc.open = func(string) (session, error) {
		if openErr != nil {
			return nil, openErr
		}
		return sess, nil
	}
	cc.now = c.now
	return cc
}

func cred() credential.Credential {
	pw := "s3cret"
	return credential.Credential{Host: "10.0.1.7", Port: 5432, Username: "app", Password: &pw, Database: "appdb"}
}

func newClock() *clock { return &clock{t: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)} }

// 1. Reachable: healthy, up, every supported metric collected.
func TestContainer_ReachableCollectsEveryMetric(t *testing.T) {
	c := newClock()
	sess := healthyDB(1000)
	cc := collectorFor(sess, nil, c)

	m, failure := cc.Collect(cred())

	assert.Nil(t, failure)
	require.NotNil(t, m.Up)
	assert.True(t, *m.Up)
	assert.Equal(t, metrics.HealthHealthy, m.Health)
	assert.Equal(t, metrics.CollectionComplete, m.CollectionStatus)
	assert.Empty(t, m.CollectionError)
	assert.Equal(t, 3, *m.ConnectionsTotal)
	assert.Equal(t, 100, *m.MaxConnections)
	assert.Equal(t, 99.5, *m.CacheHitRatio)
	assert.Equal(t, 0, *m.ActiveReplicaCount)
	require.NotNil(t, m.LastSuccessAt)
	assert.True(t, sess.closed, "one connection per collection, always closed")
}

// 2. Down: nothing listens on the address. Confirmed unavailable.
func TestContainer_RefusedIsUnhealthyAndDown(t *testing.T) {
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: &osSyscallErr{syscall.ECONNREFUSED}}
	cc := collectorFor(&fakeSession{errs: map[string]error{qHealth: refused}}, nil, newClock())

	m, failure := cc.Collect(cred())

	require.NotNil(t, m.Up)
	assert.False(t, *m.Up)
	assert.Equal(t, metrics.HealthUnhealthy, m.Health)
	assert.Equal(t, metrics.CollectionFailed, m.CollectionStatus)
	assert.Equal(t, "connection_refused", m.CollectionError)
	assert.Equal(t, "connect", failure.Stage)
	assertNoMetrics(t, m)
}

// A server that is starting up or full refuses the health query: it is not
// serving, so that is unhealthy, not unknown.
func TestContainer_StartingUpAndTooManyConnectionsAreUnhealthy(t *testing.T) {
	for code, kind := range map[pq.ErrorCode]string{"57P03": "starting_up", "53300": "too_many_connections"} {
		cc := collectorFor(&fakeSession{errs: map[string]error{qHealth: &pq.Error{Code: code}}}, nil, newClock())

		m, _ := cc.Collect(cred())

		assert.Equal(t, metrics.HealthUnhealthy, m.Health, kind)
		assert.Equal(t, kind, m.CollectionError)
	}
}

// 3. Invalid credentials: the server answered, so it is not down; the agent
// just cannot see in. Unknown, up null, never a "down" verdict.
func TestContainer_InvalidCredentialsAreUnknownNotDown(t *testing.T) {
	cc := collectorFor(&fakeSession{errs: map[string]error{qHealth: &pq.Error{Code: "28P01", Message: "password authentication failed"}}}, nil, newClock())

	m, failure := cc.Collect(cred())

	assert.Nil(t, m.Up, "up is null when health is unknown")
	assert.Equal(t, metrics.HealthUnknown, m.Health)
	assert.Equal(t, "auth_failed", m.CollectionError)
	assert.Equal(t, "connect", failure.Stage)
	assertNoMetrics(t, m)
}

// 4. SELECT 1 answers but a metrics query fails: healthy, partial, and the
// failed metric is null while the others are reported.
func TestContainer_QueryFailureOnAHealthyDatabaseIsPartial(t *testing.T) {
	sess := healthyDB(1000)
	sess.errs = map[string]error{qCacheHit: errors.New("division by something")}
	cc := collectorFor(sess, nil, newClock())

	m, failure := cc.Collect(cred())

	assert.True(t, *m.Up)
	assert.Equal(t, metrics.HealthHealthy, m.Health)
	assert.Equal(t, metrics.CollectionPartial, m.CollectionStatus)
	assert.Equal(t, "query_failed", m.CollectionError)
	assert.Nil(t, m.CacheHitRatio)
	assert.Equal(t, 3, *m.ConnectionsTotal)
	assert.Equal(t, "query", failure.Stage)
	assert.Equal(t, "cache_hit_ratio", failure.Metric)
	assert.Nil(t, m.LastSuccessAt, "a partial collection is not a success")
}

// 5. No permission on the statistics views.
func TestContainer_PermissionDeniedOnStatisticsIsPartial(t *testing.T) {
	denied := &pq.Error{Code: "42501", Message: "permission denied for view pg_stat_database"}
	sess := healthyDB(1000)
	sess.errs = map[string]error{qCacheHit: denied, qStats: denied}
	cc := collectorFor(sess, nil, newClock())

	m, _ := cc.Collect(cred())

	assert.Equal(t, metrics.HealthHealthy, m.Health)
	assert.Equal(t, metrics.CollectionPartial, m.CollectionStatus)
	assert.Equal(t, "permission_denied", m.CollectionError)
	assert.Nil(t, m.CacheHitRatio)
	assert.Nil(t, m.TransactionsPerSecond)
	assert.Equal(t, 3, *m.ConnectionsTotal)
}

// Every metrics query failing on a database that answered SELECT 1 is a
// failed collection on a healthy database, not a down one.
func TestContainer_AllQueriesFailingIsFailedButHealthy(t *testing.T) {
	boom := errors.New("boom")
	sess := healthyDB(1000)
	sess.errs = map[string]error{qConnections: boom, qMaxConn: boom, qCacheHit: boom, qStats: boom, qInRecovery: boom}
	cc := collectorFor(sess, nil, newClock())

	m, _ := cc.Collect(cred())

	assert.True(t, *m.Up)
	assert.Equal(t, metrics.HealthHealthy, m.Health)
	assert.Equal(t, metrics.CollectionFailed, m.CollectionStatus)
}

// 6. A genuine zero is 0, not null: an idle database with no other clients.
func TestContainer_GenuineZeroIsReportedAsZero(t *testing.T) {
	c := newClock()
	sess := healthyDB(1000)
	sess.rows[qConnections] = []any{0}
	cc := collectorFor(sess, nil, c)
	cc.Collect(cred())
	c.advance(5 * time.Second)

	m, _ := cc.Collect(cred()) // counters unchanged

	require.NotNil(t, m.ConnectionsTotal)
	assert.Equal(t, 0, *m.ConnectionsTotal)
	require.NotNil(t, m.TransactionsPerSecond)
	assert.Equal(t, 0.0, *m.TransactionsPerSecond)

	body, _ := json.Marshal(m)
	assert.Contains(t, string(body), `"connections_total":0`)
	assert.Contains(t, string(body), `"transactions_per_second":0`)
}

// 7. A metric with no value: no block was ever read, so there is no hit
// ratio. Null, never the made-up 100.
func TestContainer_UnavailableMetricIsNull(t *testing.T) {
	sess := healthyDB(1000)
	sess.rows[qCacheHit] = []any{sql.NullFloat64{}}
	cc := collectorFor(sess, nil, newClock())

	m, _ := cc.Collect(cred())

	assert.Nil(t, m.CacheHitRatio)
	assert.Equal(t, metrics.CollectionComplete, m.CollectionStatus, "no data is not a failure")
	body, _ := json.Marshal(m)
	assert.Contains(t, string(body), `"cache_hit_ratio":null`)
}

// 8. No replication: lag is null (not applicable), not a healthy-looking 0.
func TestContainer_NoReplicationLagIsNull(t *testing.T) {
	cc := collectorFor(healthyDB(1000), nil, newClock())

	m, _ := cc.Collect(cred())

	assert.Nil(t, m.ReplicationLagSeconds)
	assert.Equal(t, 0, *m.ActiveReplicaCount)
	body, _ := json.Marshal(m)
	assert.Contains(t, string(body), `"replication_lag_seconds":null`)
}

func TestContainer_PrimaryWithStreamingReplicaReportsLag(t *testing.T) {
	sess := healthyDB(1000)
	sess.rows[qStreaming] = []any{1}
	sess.rows[qPrimaryLag] = []any{sql.NullInt64{Int64: 2, Valid: true}}
	cc := collectorFor(sess, nil, newClock())

	m, _ := cc.Collect(cred())

	assert.Equal(t, 2, *m.ReplicationLagSeconds)
	assert.Equal(t, 1, *m.ActiveReplicaCount)
}

func TestContainer_ReplicaReportsReceiverAndLag(t *testing.T) {
	sess := healthyDB(1000)
	sess.rows[qInRecovery] = []any{true}
	sess.rows[qWalReceiver] = []any{sql.NullString{String: "streaming", Valid: true}}
	sess.rows[qReplayLag] = []any{sql.NullInt64{Int64: 4, Valid: true}}
	cc := collectorFor(sess, nil, newClock())

	m, _ := cc.Collect(cred())

	assert.True(t, *m.ReplicationConnected)
	assert.Equal(t, 4, *m.ReplicationLagSeconds)
	assert.Nil(t, m.ActiveReplicaCount)
}

// 9. Rates come from two samples and the time between them; the first
// sample, a counter reset, a gap too long to be a rate and a missing sample
// are handled.
func TestContainer_RatesFromConsecutiveSamples(t *testing.T) {
	c := newClock()
	sess := healthyDB(1000)
	cc := collectorFor(sess, nil, c)

	first, _ := cc.Collect(cred())
	assert.Nil(t, first.TransactionsPerSecond, "one sample is not a rate")

	c.advance(10 * time.Second)
	sess.rows[qStats] = []any{int64(1100), int64(20), int64(550), sql.NullTime{Time: statsReset, Valid: true}}
	second, _ := cc.Collect(cred())

	assert.Equal(t, 11.0, *second.TransactionsPerSecond) // (100 commits + 10 rollbacks) / 10s
	assert.Equal(t, 10.0, *second.CommittedTxPerSecond)
	assert.Equal(t, 5.0, *second.BlocksReadPerSecond)
}

func TestContainer_CounterResetStartsANewBaseline(t *testing.T) {
	c := newClock()
	sess := healthyDB(1000)
	cc := collectorFor(sess, nil, c)
	cc.Collect(cred())

	// pg_stat_reset(): stats_reset moves and the counters drop.
	c.advance(5 * time.Second)
	sess.rows[qStats] = []any{int64(5), int64(0), int64(1), sql.NullTime{Time: statsReset.Add(time.Hour), Valid: true}}
	afterReset, _ := cc.Collect(cred())
	assert.Nil(t, afterReset.TransactionsPerSecond, "never a negative rate across a reset")

	// A counter going backwards without a new stats_reset (a dropped database) is a reset too.
	c.advance(5 * time.Second)
	sess.rows[qStats] = []any{int64(2), int64(0), int64(1), sql.NullTime{Time: statsReset.Add(time.Hour), Valid: true}}
	decreased, _ := cc.Collect(cred())
	assert.Nil(t, decreased.TransactionsPerSecond)

	c.advance(5 * time.Second)
	sess.rows[qStats] = []any{int64(12), int64(0), int64(1), sql.NullTime{Time: statsReset.Add(time.Hour), Valid: true}}
	next, _ := cc.Collect(cred())
	assert.Equal(t, 2.0, *next.TransactionsPerSecond)
}

func TestContainer_MissingSampleAndStaleBaseline(t *testing.T) {
	c := newClock()
	sess := healthyDB(1000)
	cc := collectorFor(sess, nil, c)
	cc.Collect(cred())

	// A failed sample leaves the baseline alone; the next good one spans both intervals.
	c.advance(5 * time.Second)
	sess.errs = map[string]error{qStats: errors.New("timeout")}
	missed, _ := cc.Collect(cred())
	assert.Nil(t, missed.TransactionsPerSecond)

	c.advance(5 * time.Second)
	sess.errs = nil
	sess.rows[qStats] = []any{int64(1100), int64(10), int64(500), sql.NullTime{Time: statsReset, Valid: true}}
	spanned, _ := cc.Collect(cred())
	assert.Equal(t, 10.0, *spanned.TransactionsPerSecond) // 100 over 10s

	// A baseline older than maxSampleGap is not a rate any more.
	c.advance(maxSampleGap + time.Second)
	sess.rows[qStats] = []any{int64(9100), int64(10), int64(500), sql.NullTime{Time: statsReset, Valid: true}}
	stale, _ := cc.Collect(cred())
	assert.Nil(t, stale.TransactionsPerSecond)
}

// Each container keeps its own baseline: two databases' counters never mix.
func TestContainer_EachCollectorKeepsItsOwnBaseline(t *testing.T) {
	c := newClock()
	a := collectorFor(healthyDB(1000), nil, c)
	b := collectorFor(healthyDB(900000), nil, c)
	a.Collect(cred())
	b.Collect(cred())

	c.advance(10 * time.Second)
	m, _ := a.Collect(cred())

	assert.Equal(t, 0.0, *m.TransactionsPerSecond)
}

// 10. A timeout is not a verdict: unknown, up null, whether it hits the
// connection or a query on a healthy database.
func TestContainer_TimeoutIsUnknownNotDown(t *testing.T) {
	for _, err := range []error{
		context.DeadlineExceeded,
		&net.OpError{Op: "dial", Net: "tcp", Err: timeoutErr{}},
		&pq.Error{Code: "57014", Message: "canceling statement due to user request"},
	} {
		cc := collectorFor(&fakeSession{errs: map[string]error{qHealth: err}}, nil, newClock())

		m, _ := cc.Collect(cred())

		assert.Nil(t, m.Up, "%v", err)
		assert.Equal(t, metrics.HealthUnknown, m.Health, "%v", err)
		assert.Equal(t, "timeout", m.CollectionError, "%v", err)
	}

	sess := healthyDB(1000)
	sess.errs = map[string]error{qStats: context.DeadlineExceeded}
	m, _ := collectorFor(sess, nil, newClock()).Collect(cred())
	assert.Equal(t, metrics.HealthHealthy, m.Health)
	assert.Equal(t, "timeout", m.CollectionError)
}

func TestContainer_NetworkAndDNSFailuresAreUnknown(t *testing.T) {
	for kind, err := range map[string]error{
		"dns":     &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "db"}},
		"network": &net.OpError{Op: "dial", Err: &osSyscallErr{syscall.EHOSTUNREACH}},
	} {
		cc := collectorFor(&fakeSession{errs: map[string]error{qHealth: err}}, nil, newClock())

		m, _ := cc.Collect(cred())

		assert.Equal(t, metrics.HealthUnknown, m.Health, kind)
		assert.Equal(t, kind, m.CollectionError)
	}
}

// The last complete collection's time rides on later payloads, so an unknown
// state says how stale the last good reading is.
func TestContainer_LastSuccessAtSurvivesAFailure(t *testing.T) {
	c := newClock()
	sess := healthyDB(1000)
	cc := collectorFor(sess, nil, c)
	cc.Collect(cred())
	firstAt := c.now().UTC().Format(time.RFC3339)

	c.advance(time.Minute)
	sess.errs = map[string]error{qHealth: context.DeadlineExceeded}
	m, _ := cc.Collect(cred())

	require.NotNil(t, m.LastSuccessAt)
	assert.Equal(t, firstAt, *m.LastSuccessAt)
}

// The password reaches the driver quoted (spaces and quotes survive) and
// never reaches an error string the agent logs.
func TestContainerDSN_QuotesEveryValue(t *testing.T) {
	pw := `p a'ss\word`
	dsn := containerDSN(credential.Credential{Host: "10.0.1.7", Port: 5432, Username: "app", Password: &pw, Database: "my db"}, 5*time.Second)

	assert.Contains(t, dsn, `password='p a\'ss\\word'`)
	assert.Contains(t, dsn, `dbname='my db'`)
	assert.Contains(t, dsn, `connect_timeout='5'`)
	assert.Contains(t, dsn, `sslmode='disable'`)
}

func TestSanitizeError_RedactsCredentials(t *testing.T) {
	err := errors.New(`pq: bad conn host=10.0.1.7 password='hunter2' user=app postgres://app:hunter2@10.0.1.7/db`)

	got := SanitizeError(err)

	assert.NotContains(t, got, "hunter2")
	assert.Contains(t, got, "10.0.1.7")
}

func assertNoMetrics(t *testing.T, m metrics.PostgreSQLContainerMetrics) {
	t.Helper()
	assert.Nil(t, m.ConnectionsTotal)
	assert.Nil(t, m.MaxConnections)
	assert.Nil(t, m.CacheHitRatio)
	assert.Nil(t, m.TransactionsPerSecond)
	assert.Nil(t, m.ReplicationLagSeconds)
	body, _ := json.Marshal(m)
	assert.Contains(t, string(body), `"connections_total":null`, "unavailable, never a zero")
}

type osSyscallErr struct{ errno syscall.Errno }

func (e *osSyscallErr) Error() string { return e.errno.Error() }
func (e *osSyscallErr) Unwrap() error { return e.errno }

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }
