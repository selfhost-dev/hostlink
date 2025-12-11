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
	Collect(credential.Credential) (metrics.PostgreSQLDatabaseMetrics, error)
}

type PostgresStats struct {
	XactCommit   int64
	XactRollback int64
	BlksRead     int64
	StatsReset   time.Time // Detects pg_stat_reset() calls or crash recovery
}

type StatsCollector interface {
	QueryStats(ctx context.Context, db *sql.DB) (PostgresStats, error)
}

type Config struct {
	StatsCollector StatsCollector
}

type pgmetrics struct {
	queryTimeout   time.Duration
	statsCollector StatsCollector
	lastStats      *PostgresStats
	lastTime       time.Time
}

func New() Collector {
	return NewWithConfig(nil)
}

func NewWithConfig(cfg *Config) Collector {
	var sc StatsCollector
	if cfg != nil && cfg.StatsCollector != nil {
		sc = cfg.StatsCollector
	} else {
		sc = &defaultStatsCollector{}
	}
	return &pgmetrics{
		queryTimeout:   10 * time.Second,
		statsCollector: sc,
	}
}

func (pgm *pgmetrics) Collect(cred credential.Credential) (metrics.PostgreSQLDatabaseMetrics, error) {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cred.Host, cred.Port, cred.Username, password, cred.Database)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return metrics.PostgreSQLDatabaseMetrics{}, fmt.Errorf("failed to connect: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), pgm.queryTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return metrics.PostgreSQLDatabaseMetrics{}, fmt.Errorf("ping failed: %w", err)
	}

	m := metrics.PostgreSQLDatabaseMetrics{}

	// Collect database metrics
	if err := pgm.collectDatabaseMetrics(ctx, db, &m); err != nil {
		return m, fmt.Errorf("database metrics collection failed: %w", err)
	}

	// Collect replication metrics
	if err := pgm.collectReplicationMetrics(ctx, db, &m); err != nil {
		// Non-fatal: replication may not be configured
	}

	return m, nil
}

func (pgm *pgmetrics) collectDatabaseMetrics(ctx context.Context, db *sql.DB, m *metrics.PostgreSQLDatabaseMetrics) error {
	// Total connections
	connQuery := `
		SELECT COUNT(*)
		FROM pg_stat_activity
		WHERE pid IS NOT NULL;
	`
	if err := db.QueryRowContext(ctx, connQuery).Scan(&m.ConnectionsTotal); err != nil {
		return fmt.Errorf("connections total: %w", err)
	}

	// Max connections
	maxConnQuery := `SELECT setting::int FROM pg_settings WHERE name = 'max_connections';`
	if err := db.QueryRowContext(ctx, maxConnQuery).Scan(&m.MaxConnections); err != nil {
		return fmt.Errorf("max connections: %w", err)
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

	// Delta-based rate metrics (TPS, blocks read/sec)
	currentStats, err := pgm.statsCollector.QueryStats(ctx, db)
	if err != nil {
		return fmt.Errorf("stats query: %w", err)
	}

	now := time.Now()

	// First collection or stats reset: store baseline, return zeros
	if pgm.lastStats == nil || !pgm.lastStats.StatsReset.Equal(currentStats.StatsReset) {
		pgm.lastStats = &currentStats
		pgm.lastTime = now
		return nil
	}

	elapsed := now.Sub(pgm.lastTime).Seconds()
	if elapsed <= 0 {
		return nil
	}

	// Calculate deltas
	deltaCommit := currentStats.XactCommit - pgm.lastStats.XactCommit
	deltaRollback := currentStats.XactRollback - pgm.lastStats.XactRollback
	deltaBlksRead := currentStats.BlksRead - pgm.lastStats.BlksRead

	m.TransactionsPerSecond = float64(deltaCommit+deltaRollback) / elapsed
	m.CommittedTxPerSecond = float64(deltaCommit) / elapsed
	m.BlocksReadPerSecond = float64(deltaBlksRead) / elapsed

	// Update baseline
	pgm.lastStats = &currentStats
	pgm.lastTime = now

	return nil
}

func (pgm *pgmetrics) collectReplicationMetrics(ctx context.Context, db *sql.DB, m *metrics.PostgreSQLDatabaseMetrics) error {
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

type defaultStatsCollector struct{}

func (d *defaultStatsCollector) QueryStats(ctx context.Context, db *sql.DB) (PostgresStats, error) {
	query := `
		SELECT
			COALESCE(SUM(xact_commit), 0) AS xact_commit,
			COALESCE(SUM(xact_rollback), 0) AS xact_rollback,
			COALESCE(SUM(blks_read), 0) AS blks_read,
			COALESCE(MIN(stats_reset), now()) AS stats_reset
		FROM pg_stat_database
		WHERE stats_reset IS NOT NULL;
	`
	var stats PostgresStats
	err := db.QueryRowContext(ctx, query).Scan(
		&stats.XactCommit,
		&stats.XactRollback,
		&stats.BlksRead,
		&stats.StatsReset,
	)
	if err != nil {
		return PostgresStats{}, fmt.Errorf("query stats: %w", err)
	}
	return stats, nil
}
