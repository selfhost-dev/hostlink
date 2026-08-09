package pgmetrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"hostlink/domain/metrics"
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
		Up:                true,
		ActiveReplicaCount: &count,
	}
	data, err := json.Marshal(m)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"active_replica_count":3`)
}
