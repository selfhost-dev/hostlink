package clickhousemetrics

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Customer activity (selfhost#6038): what a "stop when idle" policy reads. The
// agent's own queries (it collects as the platform user, every 20 s) and the
// platform's other work run as that same user, so they never count as activity.

func activityRoutes(extra ...route) []route {
	base := pressureRoutes(map[string]int64{"Query": 10})
	base[2] = okRoute("FROM system.metrics", `{"metric":"TCPConnection","value":"2"}`+"\n"+
		`{"metric":"MySQLConnection","value":"1"}`+"\n"+
		`{"metric":"HTTPConnection","value":"4"}`)
	return append(extra, base...)
}

func TestActivity_IdleSecondsRunningQueriesAndNativeConnections(t *testing.T) {
	srv := newRecordingServer(t, activityRoutes(
		okRoute("system.query_log", `{"idle":"1200"}`),
		okRoute("system.processes", `{"value":"0"}`),
	)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	require.NotNil(t, m.CustomerQueryIdleSeconds)
	assert.Equal(t, 1200, *m.CustomerQueryIdleSeconds)
	require.NotNil(t, m.CustomerRunningQueries)
	assert.Equal(t, 0, *m.CustomerRunningQueries)
	require.NotNil(t, m.NativeConnections)
	assert.Equal(t, 3, *m.NativeConnections, "TCP + MySQL, never HTTP: the platform itself connects over HTTP")
	assert.Equal(t, 6, m.ConnectionsTotal, "connections_total keeps its meaning (TCP + HTTP)")
}

func TestActivity_TheAgentsOwnUserIsNeverActivity(t *testing.T) {
	srv := newRecordingServer(t, activityRoutes(
		okRoute("system.query_log", `{"idle":"60"}`),
		okRoute("system.processes", `{"value":"0"}`),
	)...)

	_, err := New().Collect(credForServer(srv.Server))
	require.NoError(t, err)

	for _, table := range []string{"system.query_log", "system.processes"} {
		reads := srv.queriesWith(table)
		require.Len(t, reads, 1, table)
		assert.Contains(t, reads[0], "user != 'default'", "%s excludes the agent's own login", table)
	}
	idle := srv.queriesWith("system.query_log")[0]
	assert.Contains(t, idle, "type = 'QueryStart'")
	assert.Contains(t, idle, "uptime()", "a restart (a Start from the console) restarts the idle clock")
	assert.Contains(t, idle, "event_date >= yesterday()", "only the last day of the log is read")
}

func TestActivity_NoQueryLogYet_IsIdleSinceTheServerStarted(t *testing.T) {
	srv := newRecordingServer(t, activityRoutes(
		chErr("system.query_log", http.StatusNotFound, codeUnknownTable, "Code: 60. UNKNOWN_TABLE"),
		okRoute("uptime() AS value", `{"value":"95"}`),
		okRoute("system.processes", `{"value":"0"}`),
	)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	require.NotNil(t, m.CustomerQueryIdleSeconds)
	assert.Equal(t, 95, *m.CustomerQueryIdleSeconds)
}

func TestActivity_FailedReads_OmitTheKeys_CollectionSucceeds(t *testing.T) {
	srv := newRecordingServer(t, activityRoutes(
		chErr("system.query_log", http.StatusInternalServerError, 241, "Code: 241. MEMORY_LIMIT_EXCEEDED"),
		chErr("system.processes", http.StatusInternalServerError, 0, "boom"),
	)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	assert.Nil(t, m.CustomerQueryIdleSeconds, "an unread idle time is never reported as idle")
	assert.Nil(t, m.CustomerRunningQueries)
	require.NotNil(t, m.NativeConnections, "read with the always-on metrics")
}

func TestActivity_SQLStringQuotesTheUser(t *testing.T) {
	assert.Equal(t, `'o\'brien'`, sqlString("o'brien"))
	assert.Equal(t, `'a\\b'`, sqlString(`a\b`))
}
