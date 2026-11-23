package clickhousemetrics

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Customer activity (selfhost#6038): what a "stop when idle" policy reads. The
// agent's own queries (it collects as the platform user, every 20 s) and the
// platform's other work run as that same user, so they never count as activity.

const activityMetrics = `{"metric":"TCPConnection","value":"2"}` + "\n" +
	`{"metric":"MySQLConnection","value":"1"}` + "\n" + `{"metric":"HTTPConnection","value":"4"}`

// activityRoutes is a standalone node with the given activity reads in front.
func activityRoutes(extra ...route) []route {
	base := pressureRoutes(map[string]int64{"Query": 10})
	for i, rt := range base {
		if rt.match == "FROM system.metrics" {
			base[i] = okRoute("FROM system.metrics", activityMetrics)
		}
	}
	return append(append(extra, okRoute("system.merges", `{"value":0}`), okRoute("system.processes", `{"value":0}`)), base...)
}

func ago(d time.Duration) int64 { return time.Now().Add(-d).Unix() }

func atRow(at int64) string { return fmt.Sprintf(`{"at":%d}`, at) }

func partsRow(at any, uptime int64) string {
	return fmt.Sprintf(`{"at":%v,"uptime":%d}`, at, uptime)
}

func TestActivity_IdleIsSinceTheNewestCustomerQueryOrWrite(t *testing.T) {
	srv := newRecordingServer(t, activityRoutes(
		okRoute("system.query_log", atRow(ago(20*time.Minute))),
		okRoute("system.parts WHERE active AND database NOT IN", partsRow(ago(50*time.Minute), 86400*3)),
	)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	require.NotNil(t, m.CustomerIdleSeconds)
	assert.InDelta(t, 1200, *m.CustomerIdleSeconds, 3, "the newer of the query and the write")
	require.NotNil(t, m.CustomerRunningQueries)
	assert.Equal(t, 0, *m.CustomerRunningQueries)
	require.NotNil(t, m.CustomerMerges)
	assert.Equal(t, 0, *m.CustomerMerges)
	require.NotNil(t, m.CustomerConnections)
	assert.Equal(t, 3, *m.CustomerConnections, "TCP + MySQL, never HTTP: the platform itself connects over HTTP")
	assert.Equal(t, 6, m.ConnectionsTotal, "connections_total keeps its meaning (TCP + HTTP)")
}

// A Kafka-engine table, S3Queue or a refreshable view writes with no query.
func TestActivity_AWriteWithNoQueryIsActivity(t *testing.T) {
	srv := newRecordingServer(t, activityRoutes(
		okRoute("system.query_log", `{"at":null}`),
		okRoute("system.parts WHERE active AND database NOT IN", partsRow(ago(5*time.Minute), 86400*3)),
	)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	assert.InDelta(t, 300, *m.CustomerIdleSeconds, 3)
}

func TestActivity_AStartRestartsTheClock_AndADayIsTheMost(t *testing.T) {
	srv := newRecordingServer(t, activityRoutes(
		okRoute("system.query_log", atRow(ago(3*time.Hour))),
		okRoute("system.parts WHERE active AND database NOT IN", partsRow("null", 95)),
	)...)
	m, err := New().Collect(credForServer(srv.Server))
	require.NoError(t, err)
	assert.Equal(t, 95, *m.CustomerIdleSeconds, "capped at the server's uptime")

	srv = newRecordingServer(t, activityRoutes(
		okRoute("system.query_log", `{"at":null}`),
		okRoute("system.parts WHERE active AND database NOT IN", partsRow("null", 86400*9)),
	)...)
	m, err = New().Collect(credForServer(srv.Server))
	require.NoError(t, err)
	assert.Equal(t, 86400, *m.CustomerIdleSeconds, "nothing in a day reads as a day")
}

func TestActivity_TheAgentsOwnUserIsNeverActivity(t *testing.T) {
	srv := newRecordingServer(t, activityRoutes(
		okRoute("system.query_log", `{"at":null}`),
		okRoute("system.parts WHERE active AND database NOT IN", partsRow("null", 600)),
	)...)

	_, err := New().Collect(credForServer(srv.Server))
	require.NoError(t, err)

	for _, table := range []string{"system.query_log", "system.processes"} {
		reads := srv.queriesWith(table)
		require.Len(t, reads, 1, table)
		assert.Contains(t, reads[0], "user != 'default'", "%s excludes the agent's own login", table)
	}
	assert.Contains(t, srv.queriesWith("system.query_log")[0], "type = 'QueryStart'")
	assert.Contains(t, srv.queriesWith("system.merges")[0], "database != 'system'", "the logs' own merges are not customer work")
}

// The first read covers the last day; later ones only what is new (with a minute
// of overlap for query_log's flush), and the newest query seen is kept.
func TestActivity_QueryLogIsReadIncrementally(t *testing.T) {
	queryAt := ago(10 * time.Minute)
	srv := newRecordingServer(t, activityRoutes(
		okRoute("system.query_log", atRow(queryAt)),
		okRoute("system.parts WHERE active AND database NOT IN", partsRow("null", 86400*3)),
	)...)
	c := New().(*collector)
	cred := credForServer(srv.Server)
	_, err := c.Collect(cred)
	require.NoError(t, err)

	rewind(c)
	srv.setRoutes(activityRoutes(
		okRoute("system.query_log", `{"at":null}`),
		okRoute("system.parts WHERE active AND database NOT IN", partsRow("null", 86400*3)),
	)...)
	m, err := c.Collect(cred)
	require.NoError(t, err)
	assert.InDelta(t, 600, *m.CustomerIdleSeconds, 3, "the query seen on the first read still counts")

	reads := srv.queriesWith("system.query_log")
	require.Len(t, reads, 2)
	assert.InDelta(t, ago(24*time.Hour), readFrom(t, reads[0]), 5, "the first read covers the last day")
	assert.InDelta(t, ago(time.Minute), readFrom(t, reads[1]), 5, "the next read starts a minute before the previous one ended")
}

// readFrom is the start of a query_log read's window.
func readFrom(t *testing.T, query string) int64 {
	t.Helper()
	parts := strings.SplitN(query, "event_time > fromUnixTimestamp(", 2)
	require.Len(t, parts, 2, query)
	var from int64
	_, err := fmt.Sscanf(parts[1], "%d)", &from)
	require.NoError(t, err)
	return from
}

func TestActivity_NoQueryLogYet_IsIdleSinceTheServerStarted(t *testing.T) {
	srv := newRecordingServer(t, activityRoutes(
		chErr("system.query_log", http.StatusNotFound, codeUnknownTable, "Code: 60. UNKNOWN_TABLE"),
		okRoute("system.parts WHERE active AND database NOT IN", partsRow("null", 95)),
	)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	require.NotNil(t, m.CustomerIdleSeconds)
	assert.Equal(t, 95, *m.CustomerIdleSeconds)
}

func TestActivity_FailedReads_OmitTheKeys_CollectionSucceeds(t *testing.T) {
	srv := newRecordingServer(t, append([]route{
		chErr("system.query_log", http.StatusInternalServerError, 241, "Code: 241. MEMORY_LIMIT_EXCEEDED"),
		chErr("system.processes", http.StatusInternalServerError, 0, "boom"),
		chErr("system.merges", http.StatusInternalServerError, 0, "boom"),
	}, activityRoutes()...)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	assert.Nil(t, m.CustomerIdleSeconds, "an unread idle time is never reported as idle")
	assert.Nil(t, m.CustomerRunningQueries)
	assert.Nil(t, m.CustomerMerges)
	require.NotNil(t, m.CustomerConnections, "read with the always-on metrics")
}

func TestActivity_SQLStringQuotesTheUser(t *testing.T) {
	assert.Equal(t, `'o\'brien'`, sqlString("o'brien"))
	assert.Equal(t, `'a\\b'`, sqlString(`a\b`))
}
