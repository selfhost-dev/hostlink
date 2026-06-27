// Package mysqlmetrics collects metrics from a MySQL or MariaDB instance.
package mysqlmetrics

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

type Collector interface {
	Collect(credential.Credential) (metrics.MySQLDatabaseMetrics, error)
}

type mysqlCollector struct {
	queryTimeout        time.Duration
	lastQueries         *int64
	lastSlowQueries     *int64
	lastRowLockWaits    *int64
	lastTmpDiskTables   *int64
	lastSelectFullJoins *int64
	lastTime            time.Time
}

func New() Collector {
	return &mysqlCollector{queryTimeout: 10 * time.Second}
}

func (mc *mysqlCollector) Collect(cred credential.Credential) (metrics.MySQLDatabaseMetrics, error) {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}

	dbPath := "/"
	if cred.Database != "" {
		dbPath = "/" + cred.Database
	}
	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)%s?timeout=10s&readTimeout=10s",
		cred.Username, password, cred.Host, cred.Port, dbPath)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return metrics.MySQLDatabaseMetrics{}, fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), mc.queryTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return metrics.MySQLDatabaseMetrics{}, fmt.Errorf("ping: %w", err)
	}

	m := metrics.MySQLDatabaseMetrics{}

	if err := mc.collectGlobalStatus(ctx, db, &m); err != nil {
		return m, fmt.Errorf("global status: %w", err)
	}
	if err := mc.collectGlobalVariables(ctx, db, &m); err != nil {
		return m, fmt.Errorf("global variables: %w", err)
	}

	// Non-fatal: replication is optional
	_ = mc.collectReplication(ctx, db, &m)

	return m, nil
}

func (mc *mysqlCollector) collectGlobalStatus(ctx context.Context, db *sql.DB, m *metrics.MySQLDatabaseMetrics) error {
	rows, err := db.QueryContext(ctx, `
		SHOW GLOBAL STATUS WHERE Variable_name IN (
			'Threads_connected','Threads_running',
			'Queries','Slow_queries','Aborted_connects',
			'Innodb_buffer_pool_read_requests','Innodb_buffer_pool_reads',
			'Innodb_row_lock_waits','Created_tmp_disk_tables','Select_full_join'
		)
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	status := make(map[string]string)
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return err
		}
		status[name] = value
	}
	if err := rows.Err(); err != nil {
		return err
	}

	m.ConnectionsTotal = parseInt(status["Threads_connected"])
	m.ThreadsRunning = parseInt(status["Threads_running"])
	m.ConnectionsAborted = parseInt64(status["Aborted_connects"])

	readReqs := parseFloat64(status["Innodb_buffer_pool_read_requests"])
	diskReads := parseFloat64(status["Innodb_buffer_pool_reads"])
	if readReqs > 0 {
		m.InnoDBBufferPoolHitRatio = (readReqs - diskReads) / readReqs * 100
	}

	// Delta-based rates using cumulative counters
	currentQueries := parseInt64(status["Queries"])
	currentSlowQueries := parseInt64(status["Slow_queries"])
	currentRowLockWaits := parseInt64(status["Innodb_row_lock_waits"])
	currentTmpDiskTables := parseInt64(status["Created_tmp_disk_tables"])
	currentSelectFullJoins := parseInt64(status["Select_full_join"])
	now := time.Now()
	if mc.lastQueries != nil {
		elapsed := now.Sub(mc.lastTime).Seconds()
		if elapsed > 0 {
			m.QueriesPerSecond = float64(currentQueries-*mc.lastQueries) / elapsed
			m.SlowQueriesPerSecond = float64(currentSlowQueries-*mc.lastSlowQueries) / elapsed
			m.InnoDBRowLockWaitsPerSecond = float64(currentRowLockWaits-*mc.lastRowLockWaits) / elapsed
			m.TmpDiskTablesPerSecond = float64(currentTmpDiskTables-*mc.lastTmpDiskTables) / elapsed
			m.SelectFullScansPerSecond = float64(currentSelectFullJoins-*mc.lastSelectFullJoins) / elapsed
		}
	}
	mc.lastQueries = &currentQueries
	mc.lastSlowQueries = &currentSlowQueries
	mc.lastRowLockWaits = &currentRowLockWaits
	mc.lastTmpDiskTables = &currentTmpDiskTables
	mc.lastSelectFullJoins = &currentSelectFullJoins
	mc.lastTime = now

	return nil
}

func (mc *mysqlCollector) collectGlobalVariables(ctx context.Context, db *sql.DB, m *metrics.MySQLDatabaseMetrics) error {
	return db.QueryRowContext(ctx, `SELECT @@GLOBAL.max_connections`).Scan(&m.MaxConnections)
}

func (mc *mysqlCollector) collectReplication(ctx context.Context, db *sql.DB, m *metrics.MySQLDatabaseMetrics) error {
	// MySQL 8.0.22+ renamed SLAVE to REPLICA
	rows, err := db.QueryContext(ctx, "SHOW REPLICA STATUS")
	if err != nil {
		rows, err = db.QueryContext(ctx, "SHOW SLAVE STATUS")
		if err != nil {
			return err
		}
	}
	defer rows.Close()

	if !rows.Next() {
		return rows.Err() // not a replica
	}

	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	vals := make([]interface{}, len(cols))
	ptrs := make([]interface{}, len(cols))
	for i := range vals {
		ptrs[i] = &vals[i]
	}
	if err := rows.Scan(ptrs...); err != nil {
		return err
	}

	row := make(map[string]string, len(cols))
	for i, col := range cols {
		if vals[i] != nil {
			row[col] = fmt.Sprintf("%v", vals[i])
		}
	}

	// Prefer new name (MySQL 8.0.22+), fall back to old
	lagStr := row["Seconds_Behind_Source"]
	if lagStr == "" {
		lagStr = row["Seconds_Behind_Master"]
	}
	if lagStr != "" && lagStr != "<nil>" {
		lag := parseInt(lagStr)
		m.ReplicationLagSeconds = &lag
	}

	ioRunning := row["Replica_IO_Running"]
	if ioRunning == "" {
		ioRunning = row["Slave_IO_Running"]
	}
	connected := strings.EqualFold(ioRunning, "yes")
	m.ReplicationConnected = &connected

	return rows.Err()
}

func parseInt(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}

func parseInt64(s string) int64 {
	v, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return v
}

func parseFloat64(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}
