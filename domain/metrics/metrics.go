// Package metrics
package metrics

const (
	MetricTypeSystem             = "system"
	MetricTypeNetwork            = "network"
	MetricTypePostgreSQLDatabase = "postgresql.database"
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
	ConnectionsTotal      int     `json:"connections_total"`
	MaxConnections        int     `json:"max_connections"`
	CacheHitRatio         float64 `json:"cache_hit_ratio"`
	TransactionsPerSecond float64 `json:"transactions_per_second"`
	CommittedTxPerSecond  float64 `json:"committed_tx_per_second"`
	BlocksReadPerSecond   float64 `json:"blocks_read_per_second"`
	ReplicationLagSeconds int     `json:"replication_lag_seconds"`
}
