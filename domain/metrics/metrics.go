// Package metrics
package metrics

const (
	MetricTypeSystem             = "system"
	MetricTypeNetwork            = "network"
	MetricTypePostgreSQLDatabase = "postgresql.database"
	MetricTypeStorage            = "storage"
	MetricTypePgBouncer          = "pgbouncer.stats"
	MetricTypeMySQLDatabase      = "mysql.database"
	MetricTypeMongoDBDatabase    = "mongodb.database"
	MetricTypeRedis              = "redis"
	MetricTypeContainer          = "container"
	MetricTypeTraefikService     = "traefik.proxy"
)

type MetricPayload struct {
	Version     string      `json:"version"`
	TimestampMs int64       `json:"timestamp_ms"`
	Resource    Resource    `json:"resource"`
	MetricSets  []MetricSet `json:"metric_sets"`
}

type Resource struct {
	AgentID  string `json:"agent_id"`
	HostName string `json:"host_name,omitempty"`
}

type MetricSet struct {
	Type       string         `json:"type"`
	Attributes map[string]any `json:"attributes,omitempty"`
	Metrics    any            `json:"metrics"`
}

type SystemMetrics struct {
	CPUPercent       float64 `json:"cpu_percent"`
	CPUIOWaitPercent float64 `json:"cpu_io_wait_percent"`
	MemoryPercent    float64 `json:"memory_percent"`
	DiskUsagePercent float64 `json:"disk_usage_percent"`
	LoadAvg1         float64 `json:"load_avg_1"`
	LoadAvg5         float64 `json:"load_avg_5"`
	LoadAvg15        float64 `json:"load_avg_15"`
	SwapUsagePercent float64 `json:"swap_usage_percent"`
}

type NetworkMetrics struct {
	RecvBytesPerSec float64 `json:"recv_bytes_per_sec"`
	SentBytesPerSec float64 `json:"sent_bytes_per_sec"`
}

type PostgreSQLDatabaseMetrics struct {
	Up                    bool    `json:"up"`
	ConnectionsTotal      int     `json:"connections_total"`
	MaxConnections        int     `json:"max_connections"`
	CacheHitRatio         float64 `json:"cache_hit_ratio"`
	TransactionsPerSecond float64 `json:"transactions_per_second"`
	CommittedTxPerSecond  float64 `json:"committed_tx_per_second"`
	BlocksReadPerSecond   float64 `json:"blocks_read_per_second"`
	ReplicationLagSeconds int     `json:"replication_lag_seconds"`
	ReplicationConnected  *bool  `json:"replication_connected,omitempty"`
}

// PgBouncerMetrics holds aggregated connection pool statistics collected
// via the PgBouncer admin console (SHOW POOLS + SHOW STATS).
// Up is false when PgBouncer is not running or unreachable.
type PgBouncerMetrics struct {
	Up               bool    `json:"up"`
	ClientsActive    int     `json:"clients_active"`
	ClientsWaiting   int     `json:"clients_waiting"`
	ServersActive    int     `json:"servers_active"`
	ServersIdle      int     `json:"servers_idle"`
	MaxWaitMs        float64 `json:"max_wait_ms"`
	AvgQueryTimeMs   float64 `json:"avg_query_time_ms"`
	AvgWaitTimeMs    float64 `json:"avg_wait_time_ms"`
	TotalQueriesPerSec float64 `json:"total_queries_per_sec"`
	PoolCount        int     `json:"pool_count"`
}

type StorageMetrics struct {
	DiskUsedBytes           float64 `json:"disk_used_bytes"`
	DiskFreeBytes           float64 `json:"disk_free_bytes"`
	DiskTotalBytes          float64 `json:"disk_total_bytes"`
	DiskUsedPercent         float64 `json:"disk_used_percent"`
	DiskFreePercent         float64 `json:"disk_free_percent"`
	TotalUtilizationPercent float64 `json:"total_utilization_percent"`
	ReadUtilizationPercent  float64 `json:"read_utilization_percent"`
	WriteUtilizationPercent float64 `json:"write_utilization_percent"`
	ReadBytesPerSecond      float64 `json:"read_bytes_per_second"`
	WriteBytesPerSecond     float64 `json:"write_bytes_per_second"`
	ReadIOPerSecond         float64 `json:"read_io_per_second"`
	WriteIOPerSecond        float64 `json:"write_io_per_second"`
	InodesUsed              uint64  `json:"inodes_used"`
	InodesFree              uint64  `json:"inodes_free"`
	InodesTotal             uint64  `json:"inodes_total"`
	InodesUsedPercent       float64 `json:"inodes_used_percent"`
}

type StorageAttributes struct {
	MountPoint     string `json:"mount_point"`
	Device         string `json:"device"`
	FilesystemType string `json:"filesystem_type"`
	IsReadOnly     bool   `json:"is_read_only"`
}

type MySQLDatabaseMetrics struct {
	Up                    bool    `json:"up"`
	ThreadsConnected      int     `json:"threads_connected"`
	ThreadsRunning        int     `json:"threads_running"`
	MaxConnections        int     `json:"max_connections"`
	MaxUsedConnections    int64   `json:"max_used_connections"`
	QueriesPerSecond      float64 `json:"queries_per_second"`
	SlowQueries           int64   `json:"slow_queries"`
	InnoDBCacheHitRatio   float64 `json:"innodb_cache_hit_ratio"`
	ReplicationLagSeconds *int    `json:"replication_lag_seconds,omitempty"`
	ReplicationConnected  *bool   `json:"replication_connected,omitempty"`
}

type MongoDBMetrics struct {
	Up                   bool    `json:"up"`
	ConnectionsCurrent   int     `json:"connections_current"`
	ConnectionsAvailable int     `json:"connections_available"`
	OpsPerSecond         float64 `json:"ops_per_second"`
	QueriesPerSecond     float64 `json:"queries_per_second"`
	InsertsPerSecond     float64 `json:"inserts_per_second"`
	UpdatesPerSecond     float64 `json:"updates_per_second"`
	DeletesPerSecond     float64 `json:"deletes_per_second"`
	ResidentMemoryMB     int     `json:"resident_memory_mb"`
	ReplicationLagSeconds *int   `json:"replication_lag_seconds,omitempty"`
}

type RedisMetrics struct {
	Up                     bool    `json:"up"`
	ConnectedClients       int     `json:"connected_clients"`
	UsedMemoryBytes        int64   `json:"used_memory_bytes"`
	UsedMemoryRSSBytes     int64   `json:"used_memory_rss_bytes"`
	KeyspaceHitRatio       float64 `json:"keyspace_hit_ratio"`
	InstantaneousOpsPerSec int     `json:"instantaneous_ops_per_sec"`
	EvictedKeys            int64   `json:"evicted_keys"`
	ExpiredKeys            int64   `json:"expired_keys"`
	Role                   string  `json:"role"`
	ReplicationLagSeconds  *int    `json:"replication_lag_seconds,omitempty"`
	ReplicationConnected   *bool   `json:"replication_connected,omitempty"`
}

// ContainerMetrics holds resource and health data for a single Docker container.
// RestartCount, ExitCode, Status, and UptimeSeconds come from ContainerInspect
// and are the most actionable signals for a developer debugging a crashing app.
type ContainerMetrics struct {
	Up                    bool    `json:"up"`
	CPUPercent            float64 `json:"cpu_percent"`
	MemoryUsageBytes      int64   `json:"memory_usage_bytes"`
	MemoryLimitBytes      int64   `json:"memory_limit_bytes"`
	MemoryPercent         float64 `json:"memory_percent"`
	NetRxBytesPerSec      float64 `json:"net_rx_bytes_per_sec"`
	NetTxBytesPerSec      float64 `json:"net_tx_bytes_per_sec"`
	BlockReadBytesPerSec  float64 `json:"block_read_bytes_per_sec"`
	BlockWriteBytesPerSec float64 `json:"block_write_bytes_per_sec"`
	PIDs                  int     `json:"pids"`
	// Stability signals — most useful for developers
	RestartCount  int    `json:"restart_count"`
	ExitCode      int    `json:"exit_code"`
	Status        string `json:"status"`
	UptimeSeconds int64  `json:"uptime_seconds"`
}

// TraefikEntrypointMetrics holds per-entrypoint HTTP metrics scraped from
// Traefik's Prometheus endpoint. These are real user-traffic signals captured
// at the entrypoint level — not health probes.
// Up is false when Traefik is unreachable or metrics are not enabled.
// ConnectionsCurrent is reported even when there is no request traffic.
type TraefikEntrypointMetrics struct {
	Up                 bool    `json:"up"`
	ConnectionsCurrent int64   `json:"connections_current"`
	RequestsTotal      int64   `json:"requests_total"`
	RequestsPerSecond  float64 `json:"requests_per_second"`
	ErrorRate          float64 `json:"error_rate"`          // % of 4xx + 5xx
	Requests2xx        int64   `json:"requests_2xx"`
	Requests4xx        int64   `json:"requests_4xx"`
	Requests5xx        int64   `json:"requests_5xx"`
	AvgResponseTimeMs  float64 `json:"avg_response_time_ms"`
	P50ResponseTimeMs  float64 `json:"p50_response_time_ms"`
	P95ResponseTimeMs  float64 `json:"p95_response_time_ms"`
	P99ResponseTimeMs  float64 `json:"p99_response_time_ms"`
}

type TraefikEntrypointAttributes struct {
	EntrypointName string `json:"entrypoint_name"`
}

type ContainerAttributes struct {
	ContainerID          string `json:"container_id"`
	ContainerName        string `json:"container_name"`
	Image                string `json:"image"`
	CoolifyAppID         string `json:"coolify_app_id,omitempty"`
	CoolifyProjectID     string `json:"coolify_project_id,omitempty"`
	CoolifyEnvironmentID string `json:"coolify_environment_id,omitempty"`
	CoolifyType          string `json:"coolify_type,omitempty"`
	CoolifyName          string `json:"coolify_name,omitempty"`
}
