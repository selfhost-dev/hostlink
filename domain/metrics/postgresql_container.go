package metrics

// PostgreSQLContainerMetrics is the postgresql.database set for a
// Docker-discovered PostgreSQL container (a Coolify database). Same metric
// type and keys as PostgreSQLDatabaseMetrics, so the platform and the console
// read it unchanged, plus what that type cannot say:
//
//   - Health separates "the database is down" from "the agent could not tell":
//     healthy (connected and SELECT 1 answered), unhealthy (confirmed
//     unavailable: refused, starting up, too many connections), unknown
//     (timeout, network, authentication: the database may well be fine).
//   - Up follows Health: true, false, or null when unknown. Never false for
//     something the agent could not determine.
//   - A metric that could not be read is null, never a made-up 0. A real 0
//     is 0. replication_lag_seconds is null when there is no replication.
//   - CollectionStatus (complete|partial|failed) and CollectionError (a
//     category, never the raw error, which stays in the agent log) tell a
//     failed query on a healthy database apart from a failed connection.
type PostgreSQLContainerMetrics struct {
	Up                    *bool    `json:"up"`
	Health                string   `json:"health"`
	CollectionStatus      string   `json:"collection_status"`
	CollectionError       string   `json:"collection_error,omitempty"`
	CollectionDurationMs  int64    `json:"collection_duration_ms"`
	LastSuccessAt         *string  `json:"last_success_at"`
	ConnectionsTotal      *int     `json:"connections_total"`
	MaxConnections        *int     `json:"max_connections"`
	CacheHitRatio         *float64 `json:"cache_hit_ratio"`
	TransactionsPerSecond *float64 `json:"transactions_per_second"`
	CommittedTxPerSecond  *float64 `json:"committed_tx_per_second"`
	BlocksReadPerSecond   *float64 `json:"blocks_read_per_second"`
	ReplicationLagSeconds *int     `json:"replication_lag_seconds"`
	ReplicationConnected  *bool    `json:"replication_connected,omitempty"`
	ActiveReplicaCount    *int     `json:"active_replica_count,omitempty"`

	TimescaledbHypertableCount      *int64   `json:"timescaledb_hypertable_count,omitempty"`
	TimescaledbChunkCount           *int64   `json:"timescaledb_chunk_count,omitempty"`
	TimescaledbCompressedChunkCount *int64   `json:"timescaledb_compressed_chunk_count,omitempty"`
	TimescaledbCompressionRatio     *float64 `json:"timescaledb_compression_ratio,omitempty"`
	TimescaledbTotalSizeBytes       *int64   `json:"timescaledb_total_size_bytes,omitempty"`
}

const (
	HealthHealthy   = "healthy"
	HealthUnhealthy = "unhealthy"
	HealthUnknown   = "unknown"

	CollectionComplete = "complete"
	CollectionPartial  = "partial"
	CollectionFailed   = "failed"
)
