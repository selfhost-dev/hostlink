package clickhousemetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hostlink/domain/metrics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// route answers the first query whose body contains match. A non-zero code
// is sent as X-ClickHouse-Exception-Code, the way ClickHouse reports an
// exception raised before any output was written.
type route struct {
	match  string
	status int
	code   int
	body   string
}

func okRoute(match, body string) route { return route{match: match, status: http.StatusOK, body: body} }

func chErr(match string, status, code int, body string) route {
	return route{match: match, status: status, code: code, body: body}
}

// routeServer is mockClickHouseServer with ordered routes and error replies.
// Order matters: the first match wins, so callers list specific tables first.
func routeServer(t *testing.T, routes ...route) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		q := string(body)
		for _, rt := range routes {
			if strings.Contains(q, rt.match) {
				if rt.code != 0 {
					w.Header().Set("X-ClickHouse-Exception-Code", fmt.Sprint(rt.code))
				}
				w.WriteHeader(rt.status)
				fmt.Fprint(w, rt.body)
				return
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// baseRoutes serves every query the collector needs to succeed. ClickHouse's
// JSONEachRow quotes 64-bit integers by default, so counts arrive as strings.
func baseRoutes() []route {
	return []route{
		okRoute("SELECT 1", "1\n"),
		okRoute("system.metrics", `{"metric":"TCPConnection","value":"2"}`),
		okRoute("system.events", `{"event":"Query","value":"10"}`),
		okRoute("system.parts", `{"value":"7"}`),
	}
}

func collectWith(t *testing.T, extra ...route) metrics.ClickHouseDatabaseMetrics {
	t.Helper()
	srv := routeServer(t, append(extra, baseRoutes()...)...)
	m, err := New().Collect(credForServer(srv))
	require.NoError(t, err)
	return m
}

const (
	keeperUnreachableBody = "Code: 999. Coordination::Exception: All connection tries failed while connecting to ZooKeeper. nodes: 10.0.1.5:9181 (KEEPER_EXCEPTION) (version 26.8.9.10 (official build))"
	accessDeniedBody      = "Code: 497. DB::Exception: monitor: Not enough privileges. To execute this query, it's necessary to have the grant SELECT ON system.zookeeper. (ACCESS_DENIED)"
	noZooKeeperBody       = "Code: 225. DB::Exception: No ZooKeeper defined. (NO_ZOOKEEPER)"
)

func zkRoot(n string) route { return okRoute("system.zookeeper", `{"value":"`+n+`"}`) }

func replicas(tables, delay string) route {
	return okRoute("system.replicas", `{"tables":"`+tables+`","max_delay":"`+delay+`"}`)
}

var (
	noReplicas    = replicas("0", "0")
	zkNotConfig   = chErr("system.zookeeper", 500, 139, noKeeperConfigBody)
	zkUnreachable = chErr("system.zookeeper", 500, 999, keeperUnreachableBody)
)

func intp(n int) *int { return &n }

func TestCollect_Topology(t *testing.T) {
	for _, tc := range []struct {
		name            string
		routes          []route
		wantMode        string
		wantKeeper      string
		wantReplication string
		wantTables      *int
		wantConnected   bool
		wantDelay       int
	}{
		{
			name:     "standalone: no Keeper configured (26.8 NO_ELEMENTS_IN_CONFIG) and no replicated tables",
			routes:   []route{zkNotConfig, noReplicas},
			wantMode: "standalone", wantKeeper: "not_applicable", wantReplication: "not_applicable",
			wantTables: intp(0),
		},
		{
			name:     "standalone: NO_ZOOKEEPER (225) reported in the body only",
			routes:   []route{chErr("system.zookeeper", 500, 0, noZooKeeperBody), noReplicas},
			wantMode: "standalone", wantKeeper: "not_applicable", wantReplication: "not_applicable",
			wantTables: intp(0),
		},
		{
			name:     "replicated single node: Keeper reachable, replicated tables present",
			routes:   []route{zkRoot("1"), replicas("3", "0")},
			wantMode: "replicated", wantKeeper: "connected", wantReplication: "ok",
			wantTables: intp(3), wantConnected: true,
		},
		{
			name:     "replicated, lag at the threshold is still ok",
			routes:   []route{zkRoot("1"), replicas("3", "60")},
			wantMode: "replicated", wantKeeper: "connected", wantReplication: "ok",
			wantTables: intp(3), wantConnected: true, wantDelay: 60,
		},
		{
			name:     "replicated, lag over the threshold",
			routes:   []route{zkRoot("1"), replicas("3", "61")},
			wantMode: "replicated", wantKeeper: "connected", wantReplication: "lagging",
			wantTables: intp(3), wantConnected: true, wantDelay: 61,
		},
		{
			name:     "Keeper configured but unreachable (KEEPER_EXCEPTION)",
			routes:   []route{zkUnreachable, replicas("2", "0")},
			wantMode: "replicated", wantKeeper: "disconnected", wantReplication: "unknown",
			wantTables: intp(2),
		},
		{
			name:     "Keeper reachable but its root is empty: no quorum",
			routes:   []route{zkRoot("0"), replicas("2", "0")},
			wantMode: "replicated", wantKeeper: "disconnected", wantReplication: "unknown",
			wantTables: intp(2),
		},
		{
			name:     "Keeper configured and up, but no replicated tables (e.g. Keeper kept for later)",
			routes:   []route{zkRoot("1"), noReplicas},
			wantMode: "standalone", wantKeeper: "connected", wantReplication: "not_applicable",
			wantTables: intp(0), wantConnected: true,
		},
		{
			name:     "no Keeper config but replicated tables exist: they need Keeper, so it is missing, not not-applicable",
			routes:   []route{zkNotConfig, replicas("2", "0")},
			wantMode: "replicated", wantKeeper: "disconnected", wantReplication: "unknown",
			wantTables: intp(2),
		},
		{
			name:     "permission error on the probe is unknown, never not_applicable",
			routes:   []route{chErr("system.zookeeper", 403, 497, accessDeniedBody), replicas("2", "0")},
			wantMode: "replicated", wantKeeper: "unknown", wantReplication: "ok",
			wantTables: intp(2),
		},
		{
			name:     "probe error with no ClickHouse code is unknown",
			routes:   []route{chErr("system.zookeeper", 502, 0, "Bad Gateway"), noReplicas},
			wantMode: "standalone", wantKeeper: "unknown", wantReplication: "not_applicable",
			wantTables: intp(0),
		},
		{
			name: "code 139 for some other missing config element is unknown",
			routes: []route{
				chErr("system.zookeeper", 500, 139, "Code: 139. DB::Exception: No macro 'replica' in config. (NO_ELEMENTS_IN_CONFIG)"),
				noReplicas,
			},
			wantMode: "standalone", wantKeeper: "unknown", wantReplication: "not_applicable",
			wantTables: intp(0),
		},
		{
			name:     "system.replicas unreadable: mode and replication unknown, count omitted",
			routes:   []route{zkRoot("1"), chErr("system.replicas", 500, 159, "Code: 159. DB::Exception: Timeout exceeded. (TIMEOUT_EXCEEDED)")},
			wantMode: "unknown", wantKeeper: "connected", wantReplication: "unknown",
			wantConnected: true,
		},
		{
			name:     "system.replicas answers no row: unknown",
			routes:   []route{zkRoot("1"), okRoute("system.replicas", "")},
			wantMode: "unknown", wantKeeper: "connected", wantReplication: "unknown",
			wantConnected: true,
		},
		{
			name:     "standalone with an unknown Keeper verdict is still standalone",
			routes:   []route{chErr("system.zookeeper", 500, 0, "boom"), noReplicas},
			wantMode: "standalone", wantKeeper: "unknown", wantReplication: "not_applicable",
			wantTables: intp(0),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := collectWith(t, tc.routes...)

			assert.Equal(t, tc.wantMode, m.Mode, "mode")
			assert.Equal(t, tc.wantKeeper, m.KeeperStatus, "keeper_status")
			assert.Equal(t, tc.wantReplication, m.ReplicationStatus, "replication_status")
			assert.Equal(t, tc.wantTables, m.ReplicatedTablesCount, "replicated_tables_count")

			// Legacy fields keep today's values whatever the verdict.
			assert.Equal(t, tc.wantConnected, m.KeeperConnected, "keeper_connected")
			assert.Equal(t, tc.wantDelay, m.ReplicationDelay, "replication_delay")
			assert.Equal(t, tc.wantDelay, m.ReplicationLagSeconds, "replication_lag_seconds")
			assert.Nil(t, m.ActiveReplicaCount, "active_replica_count stays unset, as today")
			assert.Equal(t, 7, m.PartsActive, "unrelated metrics unaffected")
		})
	}
}

func TestCollect_DetachedParts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		routes     []route
		wantAll    int
		wantBroken *int
	}{
		{
			name: "broken part counts in both; a deliberate detach only in the legacy count",
			routes: []route{
				okRoute("countIf", `{"value":"1"}`), // broken-only query
				okRoute("system.detached_parts", `{"value":"3"}`),
			},
			wantAll: 3, wantBroken: intp(1),
		},
		{
			name: "only deliberate detaches: no broken parts",
			routes: []route{
				okRoute("countIf", `{"value":"0"}`),
				okRoute("system.detached_parts", `{"value":"2"}`),
			},
			wantAll: 2, wantBroken: intp(0),
		},
		{
			name: "broken-only query fails: new field omitted, legacy count unchanged",
			routes: []route{
				chErr("countIf", 500, 47, "Code: 47. DB::Exception: Unknown identifier"),
				okRoute("system.detached_parts", `{"value":"2"}`),
			},
			wantAll: 2, wantBroken: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := collectWith(t, append(tc.routes, zkNotConfig, noReplicas)...)
			assert.Equal(t, tc.wantAll, m.BrokenPartsCount, "broken_parts_count keeps counting every detached part")
			assert.Equal(t, tc.wantBroken, m.BrokenDetachedPartsCount, "broken_detached_parts_count")
		})
	}
}

func TestBrokenDetachedPartsQuery_FiltersOnBrokenReason(t *testing.T) {
	assert.Contains(t, brokenDetachedPartsQuery, "system.detached_parts")
	assert.Contains(t, brokenDetachedPartsQuery, "LIKE 'broken%'")
}

// The payload a standalone node sends must keep every key today's console
// and alert rules read, with today's values, plus the new verdict fields.
func TestCollect_Standalone_JSONCompatibility(t *testing.T) {
	m := collectWith(t, zkNotConfig, noReplicas,
		okRoute("countIf", `{"value":"0"}`),
		okRoute("system.detached_parts", `{"value":"0"}`))
	m.Up = true // set by the metrics pusher on a successful collect

	raw, err := json.Marshal(m)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	assert.Equal(t, false, got["keeper_connected"])
	assert.Equal(t, float64(0), got["replication_delay"])
	assert.Equal(t, float64(0), got["replication_lag_seconds"])
	assert.Equal(t, float64(0), got["broken_parts_count"])
	assert.IsType(t, float64(0), got["failed_queries_per_second"])
	assert.NotContains(t, got, "active_replica_count")

	assert.Equal(t, "standalone", got["mode"])
	assert.Equal(t, "not_applicable", got["keeper_status"])
	assert.Equal(t, "not_applicable", got["replication_status"])
	assert.Equal(t, float64(0), got["replicated_tables_count"])
	assert.Equal(t, float64(0), got["broken_detached_parts_count"])
}

// A Keeper connection attempt that outlasts the HTTP timeout surfaces as a Go
// transport error, not a ClickHouse error code: it reads unknown, the
// conservative verdict (keeper_connected stays false either way).
func TestKeeperStatus_TransportErrorIsUnknown(t *testing.T) {
	timeout := &url.Error{Op: "Post", URL: "http://127.0.0.1:8123", Err: context.DeadlineExceeded}

	assert.Equal(t, "unknown", keeperStatus(nil, timeout))
}
