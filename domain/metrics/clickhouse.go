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
}
