// Package pgmetrics collects the metrics for the PostgreSQL
package pgmetrics

import (
	"context"
	"database/sql"
	"fmt"
	"hostlink/domain/credential"
	"time"

	_ "github.com/lib/pq"
)

type DatabaseMetrics struct {
	ConnectionsTotal      int
	MaxConnections        int
	CacheHitRatio         float64
	TransactionsPerSecond float64
	ReplicationLagSeconds int
}

type Collector interface {
	Collect(credential.Credential) (DatabaseMetrics, error)
}

type pgmetrics struct {
	queryTimeout time.Duration
}

func New() Collector {
	return pgmetrics{
		queryTimeout: 10 * time.Second,
	}
}

func (pgm pgmetrics) Collect(cred credential.Credential) (DatabaseMetrics, error) {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cred.Host, cred.Port, cred.Username, password, cred.Database)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return DatabaseMetrics{}, fmt.Errorf("failed to connect: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), pgm.queryTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return DatabaseMetrics{}, fmt.Errorf("ping failed: %w", err)
	}

	m := DatabaseMetrics{}

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

func (pgm pgmetrics) collectDatabaseMetrics(ctx context.Context, db *sql.DB, m *DatabaseMetrics) error {
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

	// Transactions per second
	tpsQuery := `
		SELECT
			COALESCE(ROUND(
				(sum(xact_commit) + sum(xact_rollback))::numeric /
				NULLIF(EXTRACT(EPOCH FROM (now() - min(stats_reset))), 0)
			, 2), 0) as tps
		FROM pg_stat_database
		WHERE stats_reset IS NOT NULL;
	`
	if err := db.QueryRowContext(ctx, tpsQuery).Scan(&m.TransactionsPerSecond); err != nil {
		m.TransactionsPerSecond = 0
	}

	return nil
}

func (pgm pgmetrics) collectReplicationMetrics(ctx context.Context, db *sql.DB, m *DatabaseMetrics) error {
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
