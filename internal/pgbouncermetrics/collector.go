// Package pgbouncermetrics collects connection pool statistics from the
// PgBouncer admin console (SHOW POOLS + SHOW STATS).
// Collection is attempted via the admin database on port 6432.
// If PgBouncer is not running the collector returns Up: false without error —
// callers treat this as a soft miss, not a hard failure.
package pgbouncermetrics

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"

	_ "github.com/lib/pq"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

const (
	pgbouncerPort     = 6432
	pgbouncerDatabase = "pgbouncer"
)

// Collector collects PgBouncer pool statistics using the same agent
// credential used for PostgreSQL (selfhostadmin / same password).
type Collector interface {
	Collect(credential.Credential) (metrics.PgBouncerMetrics, error)
}

type collector struct {
	queryTimeout time.Duration
}

func New() Collector {
	return &collector{queryTimeout: 5 * time.Second}
}

func (c *collector) Collect(cred credential.Credential) (metrics.PgBouncerMetrics, error) {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}

	// Always connect to the PgBouncer admin console on port 6432,
	// regardless of the credential's port (which may already be 6432).
	connStr := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cred.Host, pgbouncerPort, cred.Username, password, pgbouncerDatabase,
	)

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return metrics.PgBouncerMetrics{}, fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), c.queryTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		// PgBouncer not running — caller marks Up: false.
		return metrics.PgBouncerMetrics{}, fmt.Errorf("ping: %w", err)
	}

	m := metrics.PgBouncerMetrics{}

	if err := c.collectPools(ctx, db, &m); err != nil {
		return m, fmt.Errorf("SHOW POOLS: %w", err)
	}

	if err := c.collectStats(ctx, db, &m); err != nil {
		// Stats are supplementary — log via caller but don't fail the whole collect.
		return m, fmt.Errorf("SHOW STATS: %w", err)
	}

	return m, nil
}

// collectPools aggregates per-pool counters from SHOW POOLS.
// Skips the internal "pgbouncer" database row.
func (c *collector) collectPools(ctx context.Context, db *sql.DB, m *metrics.PgBouncerMetrics) error {
	rows, err := db.QueryContext(ctx, "SHOW POOLS")
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	for rows.Next() {
		row := scanRowToMap(cols, rows)
		if row["database"] == "pgbouncer" {
			continue
		}

		m.PoolCount++
		m.ClientsActive += parseInt(row["cl_active"])
		m.ClientsWaiting += parseInt(row["cl_waiting"])
		m.ServersActive += parseInt(row["sv_active"])
		m.ServersIdle += parseInt(row["sv_idle"])

		// maxwait is whole seconds; maxwait_us is the sub-second remainder in
		// microseconds (PgBouncer ≥ 1.8). Combine both to get the full wait.
		waitMs := parseFloat(row["maxwait"])*1000 + parseFloat(row["maxwait_us"])/1000
		if waitMs > m.MaxWaitMs {
			m.MaxWaitMs = waitMs
		}
	}

	return rows.Err()
}

// collectStats reads aggregate throughput and latency from SHOW STATS.
// Latency averages are weighted by each database's avg_query_count so that
// high-traffic databases dominate the aggregate rather than row count.
func (c *collector) collectStats(ctx context.Context, db *sql.DB, m *metrics.PgBouncerMetrics) error {
	rows, err := db.QueryContext(ctx, "SHOW STATS")
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	// Weighted sums: weight each database's avg latency by its query rate so
	// high-traffic databases dominate the aggregate, not row count.
	var totalWeight, weightedQueryTime, weightedWaitTime float64

	for rows.Next() {
		row := scanRowToMap(cols, rows)
		if row["database"] == "pgbouncer" {
			continue
		}
		qps := parseFloat(row["avg_query_count"])
		m.TotalQueriesPerSec += qps
		weightedQueryTime += parseFloat(row["avg_query_time"]) * qps // microseconds · qps
		weightedWaitTime += parseFloat(row["avg_wait_time"]) * qps   // microseconds · qps
		totalWeight += qps
	}

	if totalWeight > 0 {
		m.AvgQueryTimeMs = weightedQueryTime / totalWeight / 1000
		m.AvgWaitTimeMs = weightedWaitTime / totalWeight / 1000
	}

	return rows.Err()
}

// scanRowToMap scans a sql.Rows row into a string map keyed by column name.
func scanRowToMap(cols []string, rows *sql.Rows) map[string]string {
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	_ = rows.Scan(ptrs...)

	result := make(map[string]string, len(cols))
	for i, col := range cols {
		if vals[i] != nil {
			result[col] = fmt.Sprintf("%v", vals[i])
		}
	}
	return result
}

func parseInt(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}
