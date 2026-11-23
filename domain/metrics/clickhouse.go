package metrics

// Topology verdicts reported on a clickhouse.database metric set (#283). They
// say whether Keeper and replication apply to this node at all, so a
// standalone node's keeper_connected=false and replication lag of 0 can be
// told apart from a replicated node that lost Keeper or is caught up.
const (
	// Mode: does this node hold any replicated table (system.replicas)?
	ClickHouseModeStandalone = "standalone"
	ClickHouseModeReplicated = "replicated"
	ClickHouseModeUnknown    = "unknown" // system.replicas could not be read

	// KeeperStatus. not_applicable only on a definite "no Keeper configured"
	// signal from the server; any other failure is unknown, so a permissions
	// error never hides a real Keeper loss.
	ClickHouseKeeperConnected     = "connected"
	ClickHouseKeeperDisconnected  = "disconnected"
	ClickHouseKeeperNotApplicable = "not_applicable"
	ClickHouseKeeperUnknown       = "unknown"

	// ReplicationStatus. not_applicable when the node has no replicated table.
	ClickHouseReplicationOK            = "ok"
	ClickHouseReplicationLagging       = "lagging"
	ClickHouseReplicationNotApplicable = "not_applicable"
	ClickHouseReplicationUnknown       = "unknown"
)

// ClickHouseDatabaseMetrics holds the key operational metrics scraped from a
// ClickHouse instance via its HTTP interface. Rate fields are per-second deltas;
// the first collection cycle stores a baseline and returns zero for all rates.
//
// Compatibility: the console reads replication_lag_seconds, replication_delay,
// broken_parts_count and failed_queries_per_second as numbers, and customer
// alert rules name these keys. Existing keys keep their name, type and value;
// the topology verdict lives only in the fields added below them.
type ClickHouseDatabaseMetrics struct {
	Up                     bool    `json:"up"`
	ConnectionsTotal       int     `json:"connections_total"`
	ConnectionsCount       int     `json:"connections_count"`
	QueryCount             int64   `json:"query_count"`
	QueriesPerSecond       float64 `json:"queries_per_second"`
	SelectQueriesPerSecond float64 `json:"select_queries_per_second"`
	SelectedRowsPerSecond  float64 `json:"selected_rows_per_second"`
	InsertQueriesPerSecond float64 `json:"insert_queries_per_second"`
	FailedQueriesPerSecond float64 `json:"failed_queries_per_second"`
	InsertedRowsPerSecond  float64 `json:"inserted_rows_per_second"`
	MemoryUsage            int64   `json:"memory_usage"`
	BackgroundMergesCount  int     `json:"background_merges_count"`

	// Customer activity, for a "stop when idle" policy (selfhost#6038). The agent
	// and the platform query as the platform user, which never counts.
	// CustomerIdleSeconds is the time since the last customer activity: a query
	// another user started (query_log) or a write to a table outside system (the
	// newest active part, which a Kafka-engine table or a refreshable view writes
	// with no query), capped at the server's uptime so a start restarts the clock
	// and at a day. CustomerRunningQueries counts their running queries
	// (system.processes), CustomerMerges the merges and mutations running on
	// tables outside system. All omitted if unread. CustomerConnections counts the
	// native, MySQL and PostgreSQL protocol connections; the platform's own
	// connections are HTTP, except a native backup or a write probe while it runs.
	CustomerIdleSeconds    *int `json:"customer_idle_seconds,omitempty"`
	CustomerRunningQueries *int `json:"customer_running_queries,omitempty"`
	CustomerMerges         *int `json:"customer_merges,omitempty"`
	CustomerConnections    *int `json:"customer_connections,omitempty"`
	// ReplicationDelay and ReplicationLagSeconds are max(absolute_delay) over
	// system.replicas. On a node with no replicated table that is 0, which is
	// "not applicable", not a measurement: read ReplicationStatus to tell.
	ReplicationDelay      int `json:"replication_delay"`
	ReplicationLagSeconds int `json:"replication_lag_seconds"`
	// ActiveReplicaCount is not populated by the ClickHouse collector, so the
	// key is never sent for ClickHouse. Its former query read a `hostname`
	// column that system.replicas does not have and always failed.
	ActiveReplicaCount *int  `json:"active_replica_count,omitempty"`
	DiskUsedBytes      int64 `json:"disk_used_bytes"`
	// BrokenPartsCount counts every row of system.detached_parts, including
	// parts detached on purpose. BrokenDetachedPartsCount is the broken subset.
	BrokenPartsCount  int     `json:"broken_parts_count"`
	MarkCacheHitRatio float64 `json:"mark_cache_hit_ratio"`
	PartsActive       int     `json:"parts_active"`

	// KeeperConnected reports whether this node's Keeper connection serves a
	// readable root: `system.zookeeper` is only readable once the keeper
	// connects to a quorum of its peers, so an unreadable root (error or empty
	// count) means quorum loss, the same primary signal the server's
	// clickhouse_keeper_readiness_check probe tests.
	// Also false when Keeper is not configured at all (a standalone node):
	// KeeperStatus tells the two apart.
	KeeperConnected bool `json:"keeper_connected"`

	// Topology verdict (#283), additive. See the ClickHouse* constants above.
	Mode              string `json:"mode"`
	KeeperStatus      string `json:"keeper_status"`
	ReplicationStatus string `json:"replication_status"`
	// ReplicatedTablesCount is count() of system.replicas; omitted when that
	// table could not be read.
	ReplicatedTablesCount *int `json:"replicated_tables_count,omitempty"`
	// BrokenDetachedPartsCount counts detached parts whose reason starts with
	// "broken" (broken, broken-on-start, broken-from-backup); omitted when
	// system.detached_parts could not be read.
	BrokenDetachedPartsCount *int `json:"broken_detached_parts_count,omitempty"`

	// Pressure (selfhost#6023), additive: why a node is under pressure, for the
	// console's S3, cache, insert and memory charts. Rates are per-second deltas
	// between collections (0 on the first, like the rates above).

	// S3 requests, all of them: ClickHouse counts every request under S3* and a
	// disk's again under DiskS3*, so S3* alone is the total. Errors exclude
	// throttling but include every other status >= 300, so the routine 404s of
	// existence checks keep them above 0; throttled counts the 429/503
	// (SlowDown) answers.
	S3ReadRequestsPerSecond  float64 `json:"s3_read_requests_per_second"`
	S3WriteRequestsPerSecond float64 `json:"s3_write_requests_per_second"`
	S3ErrorsPerSecond        float64 `json:"s3_errors_per_second"`
	S3ThrottledPerSecond     float64 `json:"s3_throttled_per_second"`

	// Filesystem (read) cache: percent of bytes read from the cache rather than
	// the source (100 when nothing was read) over the interval, or since the
	// server started on the first collection; its size and size limit; and the
	// bytes read from S3 (misses, uncached reads, table functions).
	FsCacheHitRatio          float64 `json:"fs_cache_hit_ratio"`
	FsCacheSizeBytes         int64   `json:"fs_cache_size_bytes"`
	FsCacheSizeLimitBytes    int64   `json:"fs_cache_size_limit_bytes"`
	ReadFromS3BytesPerSecond float64 `json:"read_from_s3_bytes_per_second"`

	// Insert pressure: the largest active part count in any one partition (what
	// parts_to_delay_insert / parts_to_throw_insert compare), and the inserted
	// blocks delayed or refused (TOO_MANY_PARTS) for it.
	MaxPartsInPartition      *int    `json:"max_parts_in_partition,omitempty"`
	DelayedInsertsPerSecond  float64 `json:"delayed_inserts_per_second"`
	RejectedInsertsPerSecond float64 `json:"rejected_inserts_per_second"`
	// PartsToDelayInsert is the node's live parts_to_delay_insert, and
	// MaxPartsInPartitionPercent max_parts_in_partition as a percent of it, for a
	// rule that warns before inserts slow down whatever the node's limit; both
	// omitted when the limit was never read.
	PartsToDelayInsert         *int64   `json:"parts_to_delay_insert,omitempty"`
	MaxPartsInPartitionPercent *float64 `json:"max_parts_in_partition_percent,omitempty"`
	// RejectedInserts15m counts the inserted blocks refused over the last 15
	// minutes, from the counter's steps between collections (a rule compares one
	// value; a per-second rate reads 0 between a retrying writer's attempts);
	// omitted on the first collection.
	RejectedInserts15m *int `json:"rejected_inserts_15m,omitempty"`

	// Memory: the server's effective limit (memory_usage is what it tracks), and
	// merges that failed on the memory limit (part_log MergeParts, error 241)
	// since the previous collection; omitted on the first.
	MemoryLimitBytes          *int64 `json:"memory_limit_bytes,omitempty"`
	MergesMemoryLimitFailures *int   `json:"merges_memory_limit_failures,omitempty"`
	// MergesMemoryLimitFailures15m counts the same failures over the 15 minutes that
	// end a minute ago, read on every collection, for a rule like "3 in 15 minutes"
	// (a rule compares one value); omitted if unread.
	MergesMemoryLimitFailures15m *int `json:"merges_memory_limit_failures_15m,omitempty"`
	// MemoryUsagePercent is memory_usage over memory_limit_bytes, for a rule that
	// warns before the server refuses queries and merges; omitted without a limit.
	MemoryUsagePercent *float64 `json:"memory_usage_percent,omitempty"`

	// OldestMutationAgeSeconds is the age of the oldest unfinished mutation
	// (system.mutations, is_done = 0), 0 when none; omitted if unread.
	OldestMutationAgeSeconds *int `json:"oldest_mutation_age_seconds,omitempty"`

	// Wait: the host's I/O wait (OS, percent of all cores, a ~1 s sample), and
	// the time ClickHouse threads spent ready to run but not scheduled, per second
	// per host core, as a percent: 100 is on average one waiting thread per core,
	// and above 100 the host is oversubscribed (not capped).
	IOWaitPercent  *float64 `json:"io_wait_percent,omitempty"`
	CPUWaitPercent float64  `json:"cpu_wait_percent"`

	// Replication block, sent only on a node with replicated tables (absent is
	// "not applicable"). Semantics per selfhost docs/clickhouse-fe-integration.md §4.
	TotalReplicas         *int `json:"total_replicas,omitempty"`
	ActiveReplicas        *int `json:"active_replicas,omitempty"`
	QueueSize             *int `json:"queue_size,omitempty"`
	FutureParts           *int `json:"future_parts,omitempty"`
	SessionExpiredTables  *int `json:"is_session_expired,omitempty"` // a count of tables
	LostReplicas          *int `json:"lost_replicas,omitempty"`
	NoquorumDetachedParts *int `json:"noquorum_detached_parts,omitempty"`
	// ReplicatedDataLossEvents is system.events ReplicatedDataLoss, cumulative
	// since the server started: parts replaced with an EMPTY part because no
	// replica could serve them.
	ReplicatedDataLossEvents int64 `json:"replicated_data_loss_events"`
}
