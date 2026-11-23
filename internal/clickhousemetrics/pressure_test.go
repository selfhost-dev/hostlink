package clickhousemetrics

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hostlink/domain/metrics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingServer is routeServer (topology_test.go) whose routes can change
// between collections, and which keeps every query it was sent.
type recordingServer struct {
	*httptest.Server
	mu      sync.Mutex
	routes  []route
	queries []string
}

func newRecordingServer(t *testing.T, routes ...route) *recordingServer {
	t.Helper()
	s := &recordingServer{routes: routes}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		q := string(body)
		s.mu.Lock()
		s.queries = append(s.queries, q)
		current := s.routes
		s.mu.Unlock()
		for _, rt := range current {
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
	t.Cleanup(s.Close)
	return s
}

func (s *recordingServer) setRoutes(routes ...route) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes = routes
}

// queriesWith returns the queries sent so far that contain substr.
func (s *recordingServer) queriesWith(substr string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, q := range s.queries {
		if strings.Contains(q, substr) {
			out = append(out, q)
		}
	}
	return out
}

// eventRows answers system.events; JSONEachRow quotes 64-bit integers.
func eventRows(values map[string]int64) string {
	rows := make([]string, 0, len(values))
	for name, v := range values {
		rows = append(rows, fmt.Sprintf(`{"event":%q,"value":"%d"}`, name, v))
	}
	return strings.Join(rows, "\n")
}

// pressureRoutes is a standalone node (no replicated tables) with the given counters.
func pressureRoutes(events map[string]int64) []route {
	return []route{
		okRoute("SELECT 1", "1\n"),
		chErr("system.zookeeper", http.StatusNotFound, codeUnknownTable, "Code: 60. UNKNOWN_TABLE"),
		okRoute("FROM system.metrics", `{"metric":"TCPConnection","value":"1"}`+"\n"+
			`{"metric":"FilesystemCacheSize","value":"5368709120"}`+"\n"+
			`{"metric":"FilesystemCacheSizeLimit","value":"10737418240"}`),
		okRoute("FROM system.events", eventRows(events)),
		okRoute("system.asynchronous_metrics", `{"metric":"MaxPartCountForPartition","value":812}`+"\n"+
			`{"metric":"OSIOWaitTimeNormalized","value":0.125}`),
		okRoute("system.server_settings", `{"value":"6010753514"}`),
		okRoute("system.mutations", `{"value":"4200"}`),
		okRoute("system.part_log", `{"value":"2"}`),
		okRoute("absolute_delay", `{"tables":"0","max_delay":0}`),
		okRoute("system.parts", `{"value":"42"}`),
	}
}

// rewind makes the previous collection look 10 s old, part_log window included.
func rewind(c *collector) {
	c.lastTime = c.lastTime.Add(-10 * time.Second)
	c.partLogFrom = c.partLogFrom.Add(-10 * time.Second)
}

func TestPressure_FirstCollection_GaugesNowRatesZeroMergeFailuresOmitted(t *testing.T) {
	srv := newRecordingServer(t, pressureRoutes(map[string]int64{
		"Query": 10, "S3ReadRequestsCount": 500, "ReplicatedDataLoss": 3,
		"CachedReadBufferReadFromCacheBytes": 900, "CachedReadBufferReadFromSourceBytes": 100,
	})...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	assert.Equal(t, 0.0, m.S3ReadRequestsPerSecond, "first collection is the baseline")
	assert.Equal(t, int64(5368709120), m.FsCacheSizeBytes)
	assert.Equal(t, int64(10737418240), m.FsCacheSizeLimitBytes)
	assert.Equal(t, 90.0, m.FsCacheHitRatio, "no previous collection: cumulative 900/(900+100)")
	require.NotNil(t, m.MaxPartsInPartition)
	assert.Equal(t, 812, *m.MaxPartsInPartition)
	require.NotNil(t, m.IOWaitPercent)
	assert.Equal(t, 12.5, *m.IOWaitPercent)
	require.NotNil(t, m.MemoryLimitBytes)
	assert.Equal(t, int64(6010753514), *m.MemoryLimitBytes)
	require.NotNil(t, m.MemoryUsagePercent, "memory_usage over the effective limit")
	assert.InDelta(t, 0.0, *m.MemoryUsagePercent, 0.001, "the fixture tracks no memory")
	require.NotNil(t, m.OldestMutationAgeSeconds)
	assert.Equal(t, 4200, *m.OldestMutationAgeSeconds)
	assert.Nil(t, m.MergesMemoryLimitFailures, "the first collection only opens the window")
	assert.Empty(t, srv.queriesWith("event_time > toDateTime("), "the first collection only opens the window")
	assert.Equal(t, int64(3), m.ReplicatedDataLossEvents)
	assert.Nil(t, m.TotalReplicas, "a standalone node sends no replication block")
	assert.Empty(t, srv.queriesWith("total_replicas"))
}

// S3* counts every request and DiskS3* a disk's again, so only S3* is read:
// adding DiskS3* would count disk traffic twice.
func TestPressure_SecondCollection_RatesFromS3TotalsOnly(t *testing.T) {
	srv := newRecordingServer(t, pressureRoutes(map[string]int64{"Query": 10})...)
	c := New().(*collector)
	c.cpus = 4
	cred := credForServer(srv.Server)
	_, err := c.Collect(cred)
	require.NoError(t, err)

	rewind(c)
	srv.setRoutes(pressureRoutes(map[string]int64{
		"Query":                               10,
		"S3ReadRequestsCount":                 1000,
		"DiskS3ReadRequestsCount":             900,
		"S3WriteRequestsCount":                200,
		"DiskS3WriteRequestsCount":            150,
		"S3ReadRequestsErrors":                3,
		"S3WriteRequestsErrors":               7,
		"S3ReadRequestsThrottling":            20,
		"S3WriteRequestsThrottling":           30,
		"CachedReadBufferReadFromCacheBytes":  600,
		"CachedReadBufferReadFromSourceBytes": 400,
		"ReadBufferFromS3Bytes":               20_000_000,
		"DelayedInserts":                      40,
		"RejectedInserts":                     5,
		"OSCPUWaitMicroseconds":               8_000_000,
	})...)
	m, err := c.Collect(cred)
	require.NoError(t, err)

	assert.InDelta(t, 100.0, m.S3ReadRequestsPerSecond, 0.5, "1000 / 10 s, DiskS3* not added")
	assert.InDelta(t, 20.0, m.S3WriteRequestsPerSecond, 0.1)
	assert.InDelta(t, 1.0, m.S3ErrorsPerSecond, 0.01)
	assert.InDelta(t, 5.0, m.S3ThrottledPerSecond, 0.05)
	assert.Equal(t, 60.0, m.FsCacheHitRatio, "over the interval: 600 / (600 + 400)")
	assert.InDelta(t, 2_000_000.0, m.ReadFromS3BytesPerSecond, 20_000)
	assert.InDelta(t, 4.0, m.DelayedInsertsPerSecond, 0.05)
	assert.InDelta(t, 0.5, m.RejectedInsertsPerSecond, 0.01)
	assert.InDelta(t, 20.0, m.CPUWaitPercent, 0.3, "8 s of waiting over 10 s on 4 cores")
	require.NotNil(t, m.MergesMemoryLimitFailures)
	assert.Equal(t, 2, *m.MergesMemoryLimitFailures)
}

func TestPressure_CounterReset_NeverReportsNegativeRates(t *testing.T) {
	srv := newRecordingServer(t, pressureRoutes(map[string]int64{"Query": 10, "S3ReadRequestsCount": 5000})...)
	c := New().(*collector)
	cred := credForServer(srv.Server)
	_, err := c.Collect(cred)
	require.NoError(t, err)

	rewind(c)
	srv.setRoutes(pressureRoutes(map[string]int64{"Query": 10, "S3ReadRequestsCount": 30})...) // server restarted
	m, err := c.Collect(cred)

	require.NoError(t, err)
	assert.Equal(t, 0.0, m.S3ReadRequestsPerSecond)
}

func TestPressure_NoCacheReads_IsAFullHitRatio(t *testing.T) {
	srv := newRecordingServer(t, pressureRoutes(map[string]int64{"Query": 10})...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	assert.Equal(t, 100.0, m.FsCacheHitRatio, "nothing read means nothing missed")
}

func TestPressure_OptionalReadsFail_KeysOmittedCollectionSucceeds(t *testing.T) {
	failing := []route{
		chErr("system.asynchronous_metrics", http.StatusInternalServerError, 0, "boom"),
		chErr("system.server_settings", http.StatusInternalServerError, 0, "boom"),
	}
	srv := newRecordingServer(t, append(failing, pressureRoutes(map[string]int64{"Query": 10})...)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	assert.Nil(t, m.MaxPartsInPartition)
	assert.Nil(t, m.IOWaitPercent)
	assert.Nil(t, m.MemoryLimitBytes)
	assert.Nil(t, m.MemoryUsagePercent, "no limit, no percent")
}

func TestPressure_MemoryUsagePercent_IsTrackedOverTheLimit(t *testing.T) {
	routes := append([]route{}, pressureRoutes(map[string]int64{"Query": 10})...)
	routes[2] = okRoute("FROM system.metrics", `{"metric":"MemoryTracking","value":"5409678162"}`)
	srv := newRecordingServer(t, routes...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	require.NotNil(t, m.MemoryUsagePercent)
	assert.InDelta(t, 90.0, *m.MemoryUsagePercent, 0.01, "5409678162 / 6010753514")
}

func TestPressure_NoUnfinishedMutation_IsAgeZero_AFailedReadIsOmitted(t *testing.T) {
	srv := newRecordingServer(t, append([]route{okRoute("system.mutations", `{"value":"0"}`)},
		pressureRoutes(map[string]int64{"Query": 10})...)...)
	m, err := New().Collect(credForServer(srv.Server))
	require.NoError(t, err)
	require.NotNil(t, m.OldestMutationAgeSeconds)
	assert.Equal(t, 0, *m.OldestMutationAgeSeconds)

	srv = newRecordingServer(t, append([]route{chErr("system.mutations", http.StatusInternalServerError, 0, "boom")},
		pressureRoutes(map[string]int64{"Query": 10})...)...)
	m, err = New().Collect(credForServer(srv.Server))
	require.NoError(t, err)
	assert.Nil(t, m.OldestMutationAgeSeconds)
}

func TestPressure_PartLogNotCreatedYet_CountsZeroFailures(t *testing.T) {
	srv := newRecordingServer(t, pressureRoutes(map[string]int64{"Query": 10})...)
	c := New().(*collector)
	cred := credForServer(srv.Server)
	_, err := c.Collect(cred)
	require.NoError(t, err)

	rewind(c)
	srv.setRoutes(append([]route{chErr("system.part_log", http.StatusNotFound, codeUnknownTable, "Code: 60. UNKNOWN_TABLE")},
		pressureRoutes(map[string]int64{"Query": 10})...)...)
	m, err := c.Collect(cred)

	require.NoError(t, err)
	require.NotNil(t, m.MergesMemoryLimitFailures, "part_log is lazy: no table means no merge was logged")
	assert.Equal(t, 0, *m.MergesMemoryLimitFailures)
}

// Any other failure omits the key and keeps the window's start, so the next read
// covers the failed window too: nothing is skipped.
func TestPressure_PartLogReadFails_KeyOmittedAndTheWindowIsKept(t *testing.T) {
	srv := newRecordingServer(t, pressureRoutes(map[string]int64{"Query": 10})...)
	c := New().(*collector)
	cred := credForServer(srv.Server)
	_, err := c.Collect(cred)
	require.NoError(t, err)
	rewind(c)
	windowStart := c.partLogFrom

	srv.setRoutes(append([]route{chErr("system.part_log", http.StatusInternalServerError, 241, "Code: 241. MEMORY_LIMIT_EXCEEDED")},
		pressureRoutes(map[string]int64{"Query": 10})...)...)
	m, err := c.Collect(cred)
	require.NoError(t, err)
	assert.Nil(t, m.MergesMemoryLimitFailures)
	assert.Equal(t, windowStart, c.partLogFrom, "the failed window is not given up")

	rewind(c)
	srv.setRoutes(pressureRoutes(map[string]int64{"Query": 10})...)
	m, err = c.Collect(cred)
	require.NoError(t, err)
	require.NotNil(t, m.MergesMemoryLimitFailures)
	reads := srv.queriesWith("event_time > toDateTime(") // the per-collection window reads
	require.Len(t, reads, 2)
	assert.Contains(t, reads[1], fmt.Sprintf("event_time > toDateTime(%d)", windowStart.Add(-10*time.Second).Unix()),
		"the read after a failure starts where the failed one did")
}

func replicatedRoutes(lost string) []route {
	return append([]route{
		okRoute("absolute_delay", `{"tables":"3","max_delay":0}`),
		okRoute("total_replicas", `{"total_replicas":"3","active_replicas":"2","queue_size":"5","future_parts":"1","is_session_expired":"0"}`),
		okRoute("is_lost", lost),
		okRoute("noquorum", `{"value":"4"}`),
		okRoute("system.zookeeper", `{"value":"1"}`),
	}, pressureRoutes(map[string]int64{"Query": 10})...)
}

func TestPressure_ReplicatedNode_SendsTheReplicationBlock(t *testing.T) {
	srv := newRecordingServer(t, replicatedRoutes(`{"lost":"1"}`)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	assert.Equal(t, metrics.ClickHouseModeReplicated, m.Mode)
	for name, got := range map[string]*int{
		"total_replicas": m.TotalReplicas, "active_replicas": m.ActiveReplicas, "queue_size": m.QueueSize,
		"future_parts": m.FutureParts, "is_session_expired": m.SessionExpiredTables,
		"lost_replicas": m.LostReplicas, "noquorum_detached_parts": m.NoquorumDetachedParts,
	} {
		require.NotNil(t, got, name)
	}
	assert.Equal(t, 3, *m.TotalReplicas)
	assert.Equal(t, 2, *m.ActiveReplicas)
	assert.Equal(t, 5, *m.QueueSize)
	assert.Equal(t, 1, *m.FutureParts)
	assert.Equal(t, 0, *m.SessionExpiredTables)
	assert.Equal(t, 1, *m.LostReplicas)
	assert.Equal(t, 4, *m.NoquorumDetachedParts)
}

// The lost-replica read walks Keeper for every table, so it runs once every
// lostReplicasEvery collections and the answer is reused in between.
func TestPressure_LostReplicas_ReadOnceEveryFifteenCollections(t *testing.T) {
	srv := newRecordingServer(t, replicatedRoutes(`{"lost":"1"}`)...)
	c := New()
	cred := credForServer(srv.Server)

	for i := 0; i < lostReplicasEvery; i++ {
		m, err := c.Collect(cred)
		require.NoError(t, err)
		require.NotNil(t, m.LostReplicas, "collection %d reuses the count", i+1)
		assert.Equal(t, 1, *m.LostReplicas)
	}
	assert.Len(t, srv.queriesWith("is_lost"), 1)

	_, err := c.Collect(cred)
	require.NoError(t, err)
	assert.Len(t, srv.queriesWith("is_lost"), 2, "read again on collection 16")
}

func TestPressure_LostReplicas_FailedReadIsRetriedNextCollection(t *testing.T) {
	srv := newRecordingServer(t, append([]route{chErr("is_lost", http.StatusInternalServerError, 999, "Keeper down")},
		replicatedRoutes(`{"lost":"1"}`)...)...)
	c := New()
	cred := credForServer(srv.Server)

	m, err := c.Collect(cred)
	require.NoError(t, err)
	assert.Nil(t, m.LostReplicas)

	srv.setRoutes(replicatedRoutes(`{"lost":"0"}`)...)
	m, err = c.Collect(cred)
	require.NoError(t, err)
	require.NotNil(t, m.LostReplicas)
	assert.Equal(t, 0, *m.LostReplicas)
}

func TestToFloat64(t *testing.T) {
	assert.Equal(t, 0.125, toFloat64(0.125))
	assert.Equal(t, 100.0, toFloat64(int64(100)))
	assert.Equal(t, 7.0, toFloat64(int(7)))
	assert.Equal(t, 6010753514.0, toFloat64("6010753514"))
	assert.Equal(t, 0.0, toFloat64("not a number"))
	assert.Equal(t, 0.0, toFloat64(nil))
}

// A rule compares one value to a threshold, so "at least 3 in 15 minutes" needs the
// count over 15 minutes, read on every collection (ending a minute ago, like the
// per-collection window, for part_log's late flush).
func TestPressure_MergeMemoryFailuresOver15Minutes_ReadEveryCollection(t *testing.T) {
	srv := newRecordingServer(t, append([]route{okRoute("INTERVAL 15 MINUTE", `{"value":"4"}`)},
		pressureRoutes(map[string]int64{"Query": 10})...)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	require.NotNil(t, m.MergesMemoryLimitFailures15m, "read from the first collection on")
	assert.Equal(t, 4, *m.MergesMemoryLimitFailures15m)
	reads := srv.queriesWith("INTERVAL 15 MINUTE")
	require.Len(t, reads, 1)
	assert.Contains(t, reads[0], "event_time <= now() - INTERVAL 60 SECOND")
}

// selfhost#6024: the parts rule compares against the node's live
// parts_to_delay_insert (selfhost#6012 moved it from 150 to 1000; older nodes
// keep 150 until their config is rewritten).
func TestPressure_MaxPartsInPartition_AsAPercentOfTheLiveDelayLimit(t *testing.T) {
	srv := newRecordingServer(t, append([]route{okRoute("system.merge_tree_settings", `{"value":"1000"}`)},
		pressureRoutes(map[string]int64{"Query": 10})...)...)

	m, err := New().Collect(credForServer(srv.Server))

	require.NoError(t, err)
	require.NotNil(t, m.PartsToDelayInsert)
	assert.Equal(t, int64(1000), *m.PartsToDelayInsert)
	require.NotNil(t, m.MaxPartsInPartitionPercent)
	assert.InDelta(t, 81.2, *m.MaxPartsInPartitionPercent, 0.01, "812 of 1000")

	srv.setRoutes(append([]route{okRoute("system.merge_tree_settings", `{"value":"150"}`)},
		pressureRoutes(map[string]int64{"Query": 10})...)...)
	m, err = New().Collect(credForServer(srv.Server))
	require.NoError(t, err)
	assert.InDelta(t, 541.33, *m.MaxPartsInPartitionPercent, 0.01, "an older node is far past its limit")
}

// A limit read that fails under pressure keeps the last one read: the percent a
// rule watches must not drop out when the server is short of memory.
func TestPressure_LimitReadFails_TheLastLimitReadIsKept(t *testing.T) {
	routes := append([]route{okRoute("system.merge_tree_settings", `{"value":"1000"}`)},
		pressureRoutes(map[string]int64{"Query": 10})...)
	routes[3] = okRoute("FROM system.metrics", `{"metric":"MemoryTracking","value":"5409678162"}`)
	srv := newRecordingServer(t, routes...)
	c := New().(*collector)
	cred := credForServer(srv.Server)
	_, err := c.Collect(cred)
	require.NoError(t, err)

	rewind(c)
	failing := []route{
		chErr("system.server_settings", http.StatusInternalServerError, 241, "Code: 241. MEMORY_LIMIT_EXCEEDED"),
		chErr("system.merge_tree_settings", http.StatusInternalServerError, 241, "Code: 241. MEMORY_LIMIT_EXCEEDED"),
	}
	srv.setRoutes(append(failing, routes...)...)
	m, err := c.Collect(cred)

	require.NoError(t, err)
	require.NotNil(t, m.MemoryUsagePercent)
	assert.InDelta(t, 90.0, *m.MemoryUsagePercent, 0.01)
	require.NotNil(t, m.MaxPartsInPartitionPercent)
	assert.InDelta(t, 81.2, *m.MaxPartsInPartitionPercent, 0.01)
}

// selfhost#6024: refused inserts over the last 15 minutes, from the counter's
// steps, so a writer that retries now and then still reads as refused.
func TestPressure_RejectedInsertsOver15Minutes(t *testing.T) {
	srv := newRecordingServer(t, pressureRoutes(map[string]int64{"Query": 10})...)
	c := New().(*collector)
	cred := credForServer(srv.Server)
	m, err := c.Collect(cred)
	require.NoError(t, err)
	assert.Nil(t, m.RejectedInserts15m, "omitted on the first collection")

	rewind(c)
	srv.setRoutes(pressureRoutes(map[string]int64{"Query": 10, "RejectedInserts": 5})...)
	m, err = c.Collect(cred)
	require.NoError(t, err)
	require.NotNil(t, m.RejectedInserts15m)
	assert.Equal(t, 5, *m.RejectedInserts15m)

	rewind(c)
	m, err = c.Collect(cred) // no new refusal: the 5 are still in the window
	require.NoError(t, err)
	assert.Equal(t, 5, *m.RejectedInserts15m)

	for i := range c.rejectedSteps {
		c.rejectedSteps[i].at = c.rejectedSteps[i].at.Add(-16 * time.Minute)
	}
	rewind(c)
	m, err = c.Collect(cred)
	require.NoError(t, err)
	assert.Equal(t, 0, *m.RejectedInserts15m, "older than 15 minutes")
}
