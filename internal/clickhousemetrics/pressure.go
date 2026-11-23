package clickhousemetrics

import (
	"errors"
	"fmt"
	"time"

	"hostlink/domain/metrics"
)

// Pressure metrics (selfhost#6023): why a node is under pressure, read with the
// rest of clickhouse.database. Every name below was checked against a 26.8 server.

// ClickHouse counts every S3 request under S3* and a disk's request again under
// DiskS3* (PocoHTTPClient::addMetric), so the S3* counters alone are the totals.
var (
	s3ReadEvents      = []string{"S3ReadRequestsCount"}
	s3WriteEvents     = []string{"S3WriteRequestsCount"}
	s3ErrorEvents     = []string{"S3ReadRequestsErrors", "S3WriteRequestsErrors"}
	s3ThrottledEvents = []string{"S3ReadRequestsThrottling", "S3WriteRequestsThrottling"}
)

const (
	cacheHitBytesEvent    = "CachedReadBufferReadFromCacheBytes"
	cacheSourceBytesEvent = "CachedReadBufferReadFromSourceBytes"
	readFromS3BytesEvent  = "ReadBufferFromS3Bytes"
	delayedInsertsEvent   = "DelayedInserts"
	rejectedInsertsEvent  = "RejectedInserts"
	cpuWaitEvent          = "OSCPUWaitMicroseconds"
	dataLossEvent         = "ReplicatedDataLoss"
)

// pressureEvents are the counters the pressure rates read, added to eventsQuery.
var pressureEvents = append([]string{cacheHitBytesEvent, cacheSourceBytesEvent, readFromS3BytesEvent,
	delayedInsertsEvent, rejectedInsertsEvent, cpuWaitEvent, dataLossEvent},
	append(append(append(append([]string{}, s3ReadEvents...), s3WriteEvents...), s3ErrorEvents...), s3ThrottledEvents...)...)

const (
	asyncPressureQuery = "SELECT metric, value FROM system.asynchronous_metrics WHERE metric IN ('MaxPartCountForPartition','OSIOWaitTimeNormalized') FORMAT JSONEachRow"

	// The effective limit: the server reports the computed value, not the
	// configured 0 (26.8: 6010753514 on an 8 GiB host at ratio 0.9).
	memoryLimitQuery = "SELECT value FROM system.server_settings WHERE name = 'max_server_memory_usage' FORMAT JSONEachRow"

	replicationBlockQuery = "SELECT max(total_replicas) AS total_replicas, max(active_replicas) AS active_replicas, " +
		"max(queue_size) AS queue_size, max(future_parts) AS future_parts, " +
		"countIf(is_session_expired != 0) AS is_session_expired FROM system.replicas FORMAT JSONEachRow"

	noquorumPartsQuery = "SELECT count() AS value FROM system.detached_parts WHERE ifNull(reason, '') = 'noquorum' FORMAT JSONEachRow"

	// Replicas whose {zookeeper_path}/replicas/<name>/is_lost is "1". is_lost is
	// per table; counted by replica name, a replica lost for several tables
	// counts once. Not aliased "value": that would shadow the column the WHERE
	// reads (ILLEGAL_AGGREGATION on 26.8).
	lostReplicasQuery = "SELECT uniqExact(arrayElement(splitByChar('/', path), -1)) AS lost FROM system.zookeeper " +
		"WHERE name = 'is_lost' AND value = '1' AND path IN (" +
		"SELECT concat(path, '/', name) FROM system.zookeeper WHERE path IN (" +
		"SELECT DISTINCT concat(zookeeper_path, '/replicas') FROM system.replicas)) FORMAT JSONEachRow"

	// The lost-replica read lists every replica's znodes for every replicated
	// table from Keeper, so it runs once every this many collections (about 5
	// minutes at the 20 s cadence) and the answer is reused in between. A replica
	// re-cloning takes far longer than that.
	lostReplicasEvery = 15

	// part_log flushes every 7.5 s, so a merge's row can land after a window that
	// ends "now" was already read. Each window ends this far in the past instead:
	// windows stay contiguous and a row is counted once, about a minute late. A row
	// flushed more than this late (a server starved of memory) is missed: the count
	// can be low, never double.
	partLogLag = 60 * time.Second
)

func mergeMemoryFailuresQuery(from, to time.Time) string {
	return fmt.Sprintf("SELECT count() AS value FROM system.part_log WHERE event_type = 'MergeParts' AND error = 241 "+
		"AND event_time > toDateTime(%d) AND event_time <= toDateTime(%d) FORMAT JSONEachRow", from.Unix(), to.Unix())
}

// applyPressureRates turns the counters into the pressure rates, against the
// previous collection's counters. A counter that went down (the server
// restarted) counts as 0, never a negative rate.
func (c *collector) applyPressureRates(m *metrics.ClickHouseDatabaseMetrics, events map[string]int64, now time.Time) {
	prev := c.lastEvents
	elapsed := now.Sub(c.lastTime).Seconds()
	delta := func(names ...string) float64 {
		var d int64
		for _, n := range names {
			if step := events[n] - prev[n]; step > 0 {
				d += step
			}
		}
		return float64(d)
	}

	if prev != nil && elapsed > 0 {
		m.S3ReadRequestsPerSecond = delta(s3ReadEvents...) / elapsed
		m.S3WriteRequestsPerSecond = delta(s3WriteEvents...) / elapsed
		m.S3ErrorsPerSecond = delta(s3ErrorEvents...) / elapsed
		m.S3ThrottledPerSecond = delta(s3ThrottledEvents...) / elapsed
		m.ReadFromS3BytesPerSecond = delta(readFromS3BytesEvent) / elapsed
		m.DelayedInsertsPerSecond = delta(delayedInsertsEvent) / elapsed
		m.RejectedInsertsPerSecond = delta(rejectedInsertsEvent) / elapsed
		if c.cpus > 0 {
			m.CPUWaitPercent = delta(cpuWaitEvent) / (elapsed * 1e6 * float64(c.cpus)) * 100
		}
	}

	// Over the interval when there is one, else since the server started.
	fromCache, fromSource := float64(events[cacheHitBytesEvent]), float64(events[cacheSourceBytesEvent])
	if prev != nil {
		fromCache, fromSource = delta(cacheHitBytesEvent), delta(cacheSourceBytesEvent)
	}
	if total := fromCache + fromSource; total > 0 {
		m.FsCacheHitRatio = fromCache / total * 100
	} else {
		m.FsCacheHitRatio = 100 // nothing read: nothing missed (as mark_cache_hit_ratio)
	}

	m.ReplicatedDataLossEvents = events[dataLossEvent]

	// Collect builds a fresh map each time and nothing else keeps it.
	c.lastEvents = events
}

// collectPressureReads fills the gauges that need their own query. Each is
// optional: a failed read omits its key and never fails the collection.
func (c *collector) collectPressureReads(baseURL, user, password string, m *metrics.ClickHouseDatabaseMetrics, now time.Time) {
	if rows, err := c.query(baseURL, user, password, asyncPressureQuery); err == nil {
		for _, row := range rows {
			value := toFloat64(row["value"])
			switch row["metric"] {
			case "MaxPartCountForPartition":
				n := int(value)
				m.MaxPartsInPartition = &n
			case "OSIOWaitTimeNormalized":
				pct := value * 100
				m.IOWaitPercent = &pct
			}
		}
	}

	if rows, err := c.query(baseURL, user, password, memoryLimitQuery); err == nil && len(rows) > 0 {
		if limit := toInt64(rows[0]["value"]); limit > 0 {
			m.MemoryLimitBytes = &limit
		}
	}

	c.collectMergeMemoryFailures(baseURL, user, password, m, now)
}

// collectMergeMemoryFailures counts the merges that failed on the memory limit in
// the window since the previous one ended. The first collection only opens the
// window. A failed read omits the key and keeps the window's start, so the next
// read covers the missed part too.
func (c *collector) collectMergeMemoryFailures(baseURL, user, password string, m *metrics.ClickHouseDatabaseMetrics, now time.Time) {
	windowEnd := now.Add(-partLogLag)
	if c.partLogFrom.IsZero() {
		c.partLogFrom = windowEnd
		return
	}
	if !windowEnd.After(c.partLogFrom) {
		return
	}

	rows, err := c.query(baseURL, user, password, mergeMemoryFailuresQuery(c.partLogFrom, windowEnd))
	var qErr *queryError
	// part_log is created on its first row, and it is on for platform nodes
	// (selfhost storage_config_generator.rb ENABLED_LOGS): no table, no merge logged.
	noPartLog := errors.As(err, &qErr) && qErr.Code == codeUnknownTable
	switch {
	case err == nil && len(rows) > 0:
		n := int(toInt64(rows[0]["value"]))
		m.MergesMemoryLimitFailures = &n
	case noPartLog:
		zero := 0
		m.MergesMemoryLimitFailures = &zero
	default:
		return
	}
	c.partLogFrom = windowEnd
}

// collectReplicationBlock reads the replication health block on a node with
// replicated tables. On a standalone node every key stays absent ("not
// applicable"); a key whose read fails stays absent too.
func (c *collector) collectReplicationBlock(baseURL, user, password string, m *metrics.ClickHouseDatabaseMetrics) {
	if m.Mode != metrics.ClickHouseModeReplicated {
		c.lostReplicasSkip, c.lostReplicasLast = 0, nil
		return
	}
	if rows, err := c.query(baseURL, user, password, replicationBlockQuery); err == nil && len(rows) > 0 {
		row := rows[0]
		m.TotalReplicas = intPtr(row["total_replicas"])
		m.ActiveReplicas = intPtr(row["active_replicas"])
		m.QueueSize = intPtr(row["queue_size"])
		m.FutureParts = intPtr(row["future_parts"])
		m.SessionExpiredTables = intPtr(row["is_session_expired"])
	}
	if rows, err := c.query(baseURL, user, password, noquorumPartsQuery); err == nil && len(rows) > 0 {
		m.NoquorumDetachedParts = intPtr(rows[0]["value"])
	}
	m.LostReplicas = c.lostReplicas(baseURL, user, password)
}

// lostReplicas reads the lost-replica count every lostReplicasEvery collections
// and reuses it in between. A failed read is retried on the next collection.
func (c *collector) lostReplicas(baseURL, user, password string) *int {
	if c.lostReplicasSkip > 0 {
		c.lostReplicasSkip--
		return c.lostReplicasLast
	}
	rows, err := c.query(baseURL, user, password, lostReplicasQuery)
	if err != nil || len(rows) == 0 {
		c.lostReplicasLast = nil
		return nil
	}
	c.lostReplicasLast = intPtr(rows[0]["lost"])
	c.lostReplicasSkip = lostReplicasEvery - 1
	return c.lostReplicasLast
}

func intPtr(v any) *int {
	n := int(toInt64(v))
	return &n
}
