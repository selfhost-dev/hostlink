// Package metrics
package metrics

type PostgreSQLMetrics struct {
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryPercent    float64 `json:"memory_percent"`
	LoadAvg1         float64 `json:"load_avg_1"`
	LoadAvg5         float64 `json:"load_avg_5"`
	LoadAvg15        float64 `json:"load_avg_15"`
	DiskUsagePercent float64 `json:"disk_usage_percent"`
	SwapUsagePercent float64 `json:"swap_usage_percent"`
	ConnectionsTotal int     `json:"connections_total"`
	ConnectionsPerDB struct {
		ProdDB      int `json:"prod_db"`
		AnalyticsDB int `json:"analytics_db"`
	} `json:"connections_per_db"`
	ReplicationLagSeconds int     `json:"replication_lag_seconds"`
	CacheHitRatio         float64 `json:"cache_hit_ratio"`
	TransactionsPerSecond float64 `json:"transactions_per_second"`
}
