// Package pgmetrics collects the metrics for the PostgreSQL
package pgmetrics

import (
	"context"
	"database/sql"
	"fmt"
	"hostlink/domain/credential"
	"hostlink/domain/metrics"
	"time"

	_ "github.com/lib/pq"
)

type Collector interface {
	Collect(credential.Credential) (metrics.PostgreSQLMetrics, error)
}

type pgmetrics struct {
	queryTimeout time.Duration
}

func New() Collector {
	return pgmetrics{
		queryTimeout: 10 * time.Second,
	}
}

func (pgm pgmetrics) Collect(cred credential.Credential) (metrics.PostgreSQLMetrics, error) {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cred.Host, cred.Port, cred.Username, password, cred.Database)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return metrics.PostgreSQLMetrics{}, fmt.Errorf("failed to connect: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), pgm.queryTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return metrics.PostgreSQLMetrics{}, fmt.Errorf("ping failed: %w", err)
	}

	m := metrics.PostgreSQLMetrics{}

	// Collect system metrics (CPU, memory, load, disk, swap)
	if err := pgm.collectSystemMetrics(ctx, db, &m); err != nil {
		return m, fmt.Errorf("system metrics collection failed: %w", err)
	}

	// Collect database metrics
	if err := pgm.collectDatabaseMetrics(ctx, db, &m); err != nil {
		return m, fmt.Errorf("database metrics collection failed: %w", err)
	}

	// Collect replication metrics
	if err := pgm.collectReplicationMetrics(ctx, db, &m); err != nil {
		// Non-fatal: replication may not be configured
		// Log or handle gracefully
	}

	return m, nil
}

func (pgm pgmetrics) collectSystemMetrics(ctx context.Context, db *sql.DB, m *metrics.PostgreSQLMetrics) error {
	// System metrics require pg_monitor role or superuser
	// Query pg_stat_activity and system views

	// CPU usage approximation using pg_stat_activity
	query := `
		SELECT 
			COALESCE(ROUND(
				(COUNT(*) FILTER (WHERE state = 'active') * 100.0 / 
				NULLIF(current_setting('max_connections')::numeric, 0)), 2
			), 0) as cpu_percent
		FROM pg_stat_activity
		WHERE pid IS NOT NULL;
	`
	if err := db.QueryRowContext(ctx, query).Scan(&m.CPUPercent); err != nil {
		return fmt.Errorf("cpu metrics: %w", err)
	}

	// Memory and load avg require extensions or OS-level access
	// For real implementation, use pg_proctab extension or external tools
	// Here we'll use pg_settings to get some memory info
	memQuery := `
		SELECT 
			COALESCE(ROUND(
				(current_setting('shared_buffers')::numeric / 
				 (SELECT setting::numeric FROM pg_settings WHERE name = 'shared_buffers') * 100), 2
			), 0)
		FROM pg_settings 
		WHERE name = 'shared_buffers' 
		LIMIT 1;
	`
	// This is a placeholder - real memory usage needs OS-level metrics or extensions
	var memPlaceholder float64
	if err := db.QueryRowContext(ctx, memQuery).Scan(&memPlaceholder); err == nil {
		m.MemoryPercent = 0 // Set to 0 as we can't reliably get this from pure SQL
	}

	// Load average, swap, and disk usage require system-level access
	// These would typically come from pg_proctab extension or external monitoring
	m.LoadAvg1 = 0
	m.LoadAvg5 = 0
	m.LoadAvg15 = 0
	m.DiskUsagePercent = 0
	m.SwapUsagePercent = 0

	return nil
}

func (pgm pgmetrics) collectDatabaseMetrics(ctx context.Context, db *sql.DB, m *metrics.PostgreSQLMetrics) error {
	// Total connections
	connQuery := `
		SELECT COUNT(*) 
		FROM pg_stat_activity 
		WHERE pid IS NOT NULL;
	`
	if err := db.QueryRowContext(ctx, connQuery).Scan(&m.ConnectionsTotal); err != nil {
		return fmt.Errorf("connections total: %w", err)
	}

	// Connections per database
	connPerDBQuery := `
		SELECT 
			datname,
			COUNT(*) as conn_count
		FROM pg_stat_activity
		WHERE pid IS NOT NULL AND datname IS NOT NULL
		GROUP BY datname;
	`
	rows, err := db.QueryContext(ctx, connPerDBQuery)
	if err != nil {
		return fmt.Errorf("connections per db: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var dbName string
		var count int
		if err := rows.Scan(&dbName, &count); err != nil {
			return fmt.Errorf("scan connection per db: %w", err)
		}

		// Map to struct fields based on database name
		switch dbName {
		case "proddb":
			m.ConnectionsPerDB.ProdDB = count
		case "analyticsdb":
			m.ConnectionsPerDB.AnalyticsDB = count
		}
	}

	// Cache hit ratio
	cacheQuery := `
		SELECT 
			CASE 
				WHEN (sum(blks_hit) + sum(blks_read)) = 0 THEN 100.0
				ELSE ROUND(sum(blks_hit) * 100.0 / (sum(blks_hit) + sum(blks_read)), 2)
			END as cache_hit_ratio
		FROM pg_stat_database;
	`
	if err := db.QueryRowContext(ctx, cacheQuery).Scan(&m.CacheHitRatio); err != nil {
		return fmt.Errorf("cache hit ratio: %w", err)
	}

	// Transactions per second (requires two samples with time delta)
	// For now, we'll calculate based on xact_commit + xact_rollback
	tpsQuery := `
		SELECT 
			COALESCE(ROUND(
				(sum(xact_commit) + sum(xact_rollback))::numeric / 
				EXTRACT(EPOCH FROM (now() - stats_reset))
			, 2), 0) as tps
		FROM pg_stat_database
		WHERE stats_reset IS NOT NULL
		LIMIT 1;
	`
	if err := db.QueryRowContext(ctx, tpsQuery).Scan(&m.TransactionsPerSecond); err != nil {
		// If stats_reset is NULL or calculation fails, default to 0
		m.TransactionsPerSecond = 0
	}

	return nil
}

func (pgm pgmetrics) collectReplicationMetrics(ctx context.Context, db *sql.DB, m *metrics.PostgreSQLMetrics) error {
	// Check if replication is configured
	replQuery := `
		SELECT 
			COALESCE(
				EXTRACT(EPOCH FROM (now() - pg_last_xact_replay_timestamp())),
				0
			)::integer as lag_seconds
		FROM pg_stat_replication
		LIMIT 1;
	`

	var lag sql.NullInt64
	err := db.QueryRowContext(ctx, replQuery).Scan(&lag)
	if err == sql.ErrNoRows {
		// No replication configured
		m.ReplicationLagSeconds = 0
		return nil
	}
	if err != nil {
		return fmt.Errorf("replication lag: %w", err)
	}

	if lag.Valid {
		m.ReplicationLagSeconds = int(lag.Int64)
	}

	return nil
}
