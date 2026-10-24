package metrics

// ClickHouseDatabaseMetrics holds the key operational metrics scraped from a
// ClickHouse instance via its HTTP interface. Rate fields are per-second deltas;
// the first collection cycle stores a baseline and returns zero for all rates.
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
	ReplicationDelay       int     `json:"replication_delay"`
	ReplicationLagSeconds  int     `json:"replication_lag_seconds"`
	ActiveReplicaCount     *int    `json:"active_replica_count,omitempty"`
	DiskUsedBytes          int64   `json:"disk_used_bytes"`
	BrokenPartsCount       int     `json:"broken_parts_count"`
	MarkCacheHitRatio      float64 `json:"mark_cache_hit_ratio"`
	PartsActive            int     `json:"parts_active"`

	// KeeperConnected reports whether this node's embedded ClickHouse Keeper
	// has a live raft quorum: `system.zookeeper` is only readable once the
	// keeper connects to a quorum of its peers, so an unreadable root (error
	// or empty count) means quorum loss — the same primary signal the
	// server's clickhouse_keeper_readiness_check probe tests.
	// False also when keeper is not configured (single-node / keeper-only
	// arbiters use a standalone process, so this metric is only meaningful on
	// multi-AZ data nodes).
	KeeperConnected bool `json:"keeper_connected"`
}
