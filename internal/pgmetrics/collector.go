// Package pgmetrics collects the metrics for the PostgreSQL
package pgmetrics

import (
	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

type Collector interface {
	Collect(credential.Credential) (metrics.PostgreSQLMetrics, error)
}

type pgmetrics struct{}

func New() Collector {
	return pgmetrics{}
}

func (pgm pgmetrics) Collect(cred credential.Credential) (metrics.PostgreSQLMetrics, error) {
	// TODO: calculate the metrics
	metrics := metrics.PostgreSQLMetrics{
		CPUPercent:            12.5,
		MemoryPercent:         62.4,
		LoadAvg1:              0.24,
		LoadAvg5:              0.32,
		LoadAvg15:             0.40,
		DiskUsagePercent:      78.1,
		SwapUsagePercent:      5.3,
		ConnectionsTotal:      132,
		ReplicationLagSeconds: 2,
		CacheHitRatio:         99.4,
		TransactionsPerSecond: 184.6,
	}
	metrics.ConnectionsPerDB.ProdDB = 90
	metrics.ConnectionsPerDB.AnalyticsDB = 42

	return metrics, nil
}
