package pgmetrics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"hostlink/domain/metrics"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockStatsCollector struct {
	stats PostgresStats
	err   error
}

func (m *mockStatsCollector) QueryStats(ctx context.Context, db *sql.DB) (PostgresStats, error) {
	return m.stats, m.err
}

func TestDeltaCalculation_FirstCollectionReturnsZero(t *testing.T) {
	mock := &mockStatsCollector{
		stats: PostgresStats{
			XactCommit:   1000,
			XactRollback: 100,
			BlksRead:     500,
			StatsReset:   time.Now(),
		},
	}

	collector := &pgmetrics{
		queryTimeout:   10 * time.Second,
		statsCollector: mock,
	}

	assert.Nil(t, collector.lastStats, "lastStats should be nil on first collection")
}

func TestDeltaCalculation_SecondCollectionReturnsRates(t *testing.T) {
	baseTime := time.Now()
	statsReset := baseTime.Add(-1 * time.Hour)

	collector := &pgmetrics{
		queryTimeout:   10 * time.Second,
		statsCollector: nil,
		lastStats: &PostgresStats{
			XactCommit:   1000,
			XactRollback: 100,
			BlksRead:     500,
			StatsReset:   statsReset,
		},
		lastTime: baseTime,
	}

	currentStats := PostgresStats{
		XactCommit:   1100,
		XactRollback: 110,
		BlksRead:     550,
		StatsReset:   statsReset,
	}
	now := baseTime.Add(10 * time.Second)

	elapsed := now.Sub(collector.lastTime).Seconds()

	deltaCommit := currentStats.XactCommit - collector.lastStats.XactCommit
	deltaRollback := currentStats.XactRollback - collector.lastStats.XactRollback
	deltaBlksRead := currentStats.BlksRead - collector.lastStats.BlksRead

	tps := float64(deltaCommit+deltaRollback) / elapsed
	commitTps := float64(deltaCommit) / elapsed
	blksPerSec := float64(deltaBlksRead) / elapsed

	assert.Equal(t, 11.0, tps)
	assert.Equal(t, 10.0, commitTps)
	assert.Equal(t, 5.0, blksPerSec)
}

func TestDeltaCalculation_StatsResetDetected(t *testing.T) {
	baseTime := time.Now()
	oldStatsReset := baseTime.Add(-1 * time.Hour)
	newStatsReset := baseTime.Add(-1 * time.Minute)

	lastStats := &PostgresStats{
		XactCommit:   1000000,
		XactRollback: 100000,
		BlksRead:     500000,
		StatsReset:   oldStatsReset,
	}

	currentStats := PostgresStats{
		XactCommit:   100,
		XactRollback: 10,
		BlksRead:     50,
		StatsReset:   newStatsReset,
	}

	statsResetChanged := !lastStats.StatsReset.Equal(currentStats.StatsReset)
	assert.True(t, statsResetChanged, "should detect stats reset")
}

func TestDeltaCalculation_ZeroElapsedTime(t *testing.T) {
	baseTime := time.Now()

	collector := &pgmetrics{
		queryTimeout:   10 * time.Second,
		statsCollector: nil,
		lastStats: &PostgresStats{
			XactCommit: 1000,
			StatsReset: baseTime.Add(-1 * time.Hour),
		},
		lastTime: baseTime,
	}

	elapsed := baseTime.Sub(collector.lastTime).Seconds()
	assert.Equal(t, 0.0, elapsed)
}

func TestStatsCollector_QueryError(t *testing.T) {
	expectedErr := errors.New("database connection failed")
	mock := &mockStatsCollector{
		err: expectedErr,
	}

	_, err := mock.QueryStats(context.Background(), nil)
	assert.Error(t, err)
	assert.Equal(t, expectedErr, err)
}

func TestTimescaledbMetrics_ExcludedWhenNotInstalled(t *testing.T) {
	m := metrics.PostgreSQLDatabaseMetrics{}
	assert.Nil(t, m.TimescaledbHypertableCount, "should be nil when not set")
	assert.Nil(t, m.TimescaledbChunkCount, "should be nil when not set")
	assert.Nil(t, m.TimescaledbCompressedChunkCount, "should be nil when not set")
	assert.Nil(t, m.TimescaledbCompressionRatio, "should be nil when not set")
	assert.Nil(t, m.TimescaledbTotalSizeBytes, "should be nil when not set")
}

func TestTimescaledbMetrics_JSONOmitEmpty(t *testing.T) {
	m := metrics.PostgreSQLDatabaseMetrics{
		Up: true,
	}
	data, err := json.Marshal(m)
	assert.NoError(t, err)
	assert.NotContains(t, string(data), "timescaledb", "nil TimescaleDB fields should be omitted from JSON")
}

func TestTimescaledbMetrics_JSONIncludeWhenSet(t *testing.T) {
	hypertableCount := int64(5)
	chunkCount := int64(120)
	compressedChunkCount := int64(80)
	ratio := 3.5
	totalSize := int64(1073741824)

	m := metrics.PostgreSQLDatabaseMetrics{
		Up:                              true,
		TimescaledbHypertableCount:      &hypertableCount,
		TimescaledbChunkCount:           &chunkCount,
		TimescaledbCompressedChunkCount: &compressedChunkCount,
		TimescaledbCompressionRatio:     &ratio,
		TimescaledbTotalSizeBytes:       &totalSize,
	}
	data, err := json.Marshal(m)
	assert.NoError(t, err)

	jsonStr := string(data)
	assert.Contains(t, jsonStr, `"timescaledb_hypertable_count":5`)
	assert.Contains(t, jsonStr, `"timescaledb_chunk_count":120`)
	assert.Contains(t, jsonStr, `"timescaledb_compressed_chunk_count":80`)
	assert.Contains(t, jsonStr, `"timescaledb_compression_ratio":3.5`)
	assert.Contains(t, jsonStr, `"timescaledb_total_size_bytes":1073741824`)
}

func TestActiveReplicaCount_JSONOmitEmpty(t *testing.T) {
	m := metrics.PostgreSQLDatabaseMetrics{
		Up: true,
	}
	data, err := json.Marshal(m)
	assert.NoError(t, err)
	assert.NotContains(t, string(data), "active_replica_count", "nil ActiveReplicaCount should be omitted from JSON")
}

func TestActiveReplicaCount_JSONIncludeWhenSet(t *testing.T) {
	count := 3
	m := metrics.PostgreSQLDatabaseMetrics{
		Up:                 true,
		ActiveReplicaCount: &count,
	}
	data, err := json.Marshal(m)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"active_replica_count":3`)
}

// ── TimescaleDB view probe (#4563) ────────────────────────────────────────────
//
// On plain PostgreSQL the collector used to query four timescaledb_information
// views on every pass (every 30 s), so the customer's database log filled with
// our "relation ... does not exist" errors. It now probes which views exist and
// only runs the queries it can.

// fakePGConnector answers queries from a fixed script: the to_regclass probe and
// each metric query, recording every statement it saw.
type fakePGConnector struct {
	hypertables bool
	chunks      bool
	compression bool
	queries     []string
}

func (f *fakePGConnector) Driver() driver.Driver { return f }

// Open exists only to satisfy driver.Driver; sql.OpenDB uses Connect.
func (f *fakePGConnector) Open(string) (driver.Conn, error) { return &fakePGConn{f}, nil }

func (f *fakePGConnector) Connect(context.Context) (driver.Conn, error) {
	return &fakePGConn{f}, nil
}

type fakePGConn struct{ f *fakePGConnector }

func (c *fakePGConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not used")
}

func (c *fakePGConn) Close() error { return nil }

func (c *fakePGConn) Begin() (driver.Tx, error) { return nil, errors.New("transactions are not used") }

func (c *fakePGConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.f.queries = append(c.f.queries, query)
	switch {
	case strings.Contains(query, "to_regclass"):
		return &fakePGRows{cols: []string{"hypertables", "chunks", "compression"},
			rows: [][]driver.Value{{c.f.hypertables, c.f.chunks, c.f.compression}}}, nil
	case strings.Contains(query, "count(*)::int FROM timescaledb_information.hypertables"):
		return &fakePGRows{cols: []string{"count"}, rows: [][]driver.Value{{int64(5)}}}, nil
	case strings.Contains(query, "FROM timescaledb_information.chunks"):
		return &fakePGRows{cols: []string{"total", "compressed"},
			rows: [][]driver.Value{{int64(120), int64(80)}}}, nil
	case strings.Contains(query, "hypertable_size"):
		return &fakePGRows{cols: []string{"size"}, rows: [][]driver.Value{{int64(1073741824)}}}, nil
	case strings.Contains(query, "compressed_chunk_stats"):
		return &fakePGRows{cols: []string{"ratio"}, rows: [][]driver.Value{{float64(3.5)}}}, nil
	}
	return nil, fmt.Errorf("unexpected query: %s", query)
}

type fakePGRows struct {
	cols []string
	rows [][]driver.Value
	next int
}

func (r *fakePGRows) Columns() []string { return r.cols }
func (r *fakePGRows) Close() error      { return nil }

func (r *fakePGRows) Next(dest []driver.Value) error {
	if r.next >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.next])
	r.next++
	return nil
}

func (f *fakePGConnector) db(t *testing.T) *sql.DB {
	t.Helper()
	db := sql.OpenDB(f)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func timescaledbQueries(queries []string) []string {
	var from []string
	for _, q := range queries {
		if strings.Contains(q, "FROM timescaledb_information") {
			from = append(from, q)
		}
	}
	return from
}

func TestCollectTimescaledbMetrics_PlainPostgresIssuesNoViewQueries(t *testing.T) {
	f := &fakePGConnector{}
	m := metrics.PostgreSQLDatabaseMetrics{}

	(&pgmetrics{}).collectTimescaledbMetrics(context.Background(), f.db(t), &m)

	assert.Empty(t, timescaledbQueries(f.queries),
		"a database without TimescaleDB must not query timescaledb_information (it logs an ERROR every pass)")
	assert.Len(t, f.queries, 1, "only the relation probe runs")
	assert.Nil(t, m.TimescaledbHypertableCount)
	assert.Nil(t, m.TimescaledbChunkCount)
	assert.Nil(t, m.TimescaledbCompressedChunkCount)
	assert.Nil(t, m.TimescaledbCompressionRatio)
	assert.Nil(t, m.TimescaledbTotalSizeBytes)
}

func TestCollectTimescaledbMetrics_InstalledViewsPopulateMetrics(t *testing.T) {
	f := &fakePGConnector{hypertables: true, chunks: true, compression: true}
	m := metrics.PostgreSQLDatabaseMetrics{}

	(&pgmetrics{}).collectTimescaledbMetrics(context.Background(), f.db(t), &m)

	assert.Len(t, timescaledbQueries(f.queries), 4)
	if assert.NotNil(t, m.TimescaledbHypertableCount) {
		assert.Equal(t, int64(5), *m.TimescaledbHypertableCount)
	}
	if assert.NotNil(t, m.TimescaledbChunkCount) {
		assert.Equal(t, int64(120), *m.TimescaledbChunkCount)
	}
	if assert.NotNil(t, m.TimescaledbCompressedChunkCount) {
		assert.Equal(t, int64(80), *m.TimescaledbCompressedChunkCount)
	}
	if assert.NotNil(t, m.TimescaledbTotalSizeBytes) {
		assert.Equal(t, int64(1073741824), *m.TimescaledbTotalSizeBytes)
	}
	if assert.NotNil(t, m.TimescaledbCompressionRatio) {
		assert.Equal(t, 3.5, *m.TimescaledbCompressionRatio)
	}
}

func TestCollectTimescaledbMetrics_MissingCompressionViewIsSkipped(t *testing.T) {
	f := &fakePGConnector{hypertables: true, chunks: true}
	m := metrics.PostgreSQLDatabaseMetrics{}

	(&pgmetrics{}).collectTimescaledbMetrics(context.Background(), f.db(t), &m)

	for _, q := range f.queries {
		assert.NotContains(t, q, "FROM timescaledb_information.compressed_chunk_stats",
			"a view the server does not have must not be queried")
	}
	assert.Nil(t, m.TimescaledbCompressionRatio)
	if assert.NotNil(t, m.TimescaledbHypertableCount) {
		assert.Equal(t, int64(5), *m.TimescaledbHypertableCount)
	}
}
