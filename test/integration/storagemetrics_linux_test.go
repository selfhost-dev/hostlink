//go:build integration && linux

package integration

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/internal/storagemetrics"
)

func TestStorageMetricsCollector_NewDoesNotPanic(t *testing.T) {
	collector := storagemetrics.New()
	require.NotNil(t, collector)
}

func TestStorageMetricsCollector_CollectReturnsResults(t *testing.T) {
	collector := storagemetrics.New()
	results, err := collector.Collect(context.Background())

	require.NoError(t, err)
	require.NotEmpty(t, results, "expected at least one mount point")
}

func TestStorageMetricsCollector_AttributesArePopulated(t *testing.T) {
	collector := storagemetrics.New()
	results, err := collector.Collect(context.Background())
	require.NoError(t, err)

	for _, r := range results {
		assert.NotEmpty(t, r.Attributes.MountPoint)
		assert.NotEmpty(t, r.Attributes.Device)
		assert.NotEmpty(t, r.Attributes.FilesystemType)
	}
}

func TestStorageMetricsCollector_CapacityMetricsAreSensible(t *testing.T) {
	collector := storagemetrics.New()
	results, err := collector.Collect(context.Background())
	require.NoError(t, err)

	for _, r := range results {
		m := r.Metrics
		assert.Greater(t, m.DiskTotalBytes, float64(0))
		assert.GreaterOrEqual(t, m.DiskUsedBytes, float64(0))
		assert.GreaterOrEqual(t, m.DiskFreeBytes, float64(0))
	}
}

func TestStorageMetricsCollector_PercentagesAreInValidRange(t *testing.T) {
	collector := storagemetrics.New()
	results, err := collector.Collect(context.Background())
	require.NoError(t, err)

	for _, r := range results {
		m := r.Metrics
		assert.GreaterOrEqual(t, m.DiskUsedPercent, float64(0))
		assert.LessOrEqual(t, m.DiskUsedPercent, float64(100))
		assert.GreaterOrEqual(t, m.DiskFreePercent, float64(0))
		assert.LessOrEqual(t, m.DiskFreePercent, float64(100))
		assert.GreaterOrEqual(t, m.InodesUsedPercent, float64(0))
		assert.LessOrEqual(t, m.InodesUsedPercent, float64(100))
	}
}

func TestStorageMetricsCollector_SecondCallProducesIOMetrics(t *testing.T) {
	collector := storagemetrics.New()

	_, err := collector.Collect(context.Background())
	require.NoError(t, err)

	time.Sleep(150 * time.Millisecond)

	results, err := collector.Collect(context.Background())
	require.NoError(t, err)

	for _, r := range results {
		m := r.Metrics
		assert.LessOrEqual(t, m.TotalUtilizationPercent, float64(100))
		assert.GreaterOrEqual(t, m.ReadBytesPerSecond, float64(0))
		assert.GreaterOrEqual(t, m.WriteBytesPerSecond, float64(0))
	}
}

func TestStorageMetricsCollector_NoNaNOrInfValues(t *testing.T) {
	collector := storagemetrics.New()

	_, _ = collector.Collect(context.Background())
	time.Sleep(150 * time.Millisecond)
	results, err := collector.Collect(context.Background())
	require.NoError(t, err)

	for _, r := range results {
		m := r.Metrics
		assertStorageValidFloat(t, m.DiskTotalBytes, "DiskTotalBytes")
		assertStorageValidFloat(t, m.DiskFreeBytes, "DiskFreeBytes")
		assertStorageValidFloat(t, m.DiskUsedBytes, "DiskUsedBytes")
		assertStorageValidFloat(t, m.DiskUsedPercent, "DiskUsedPercent")
		assertStorageValidFloat(t, m.DiskFreePercent, "DiskFreePercent")
		assertStorageValidFloat(t, m.InodesUsedPercent, "InodesUsedPercent")
		assertStorageValidFloat(t, m.TotalUtilizationPercent, "TotalUtilizationPercent")
		assertStorageValidFloat(t, m.ReadUtilizationPercent, "ReadUtilizationPercent")
		assertStorageValidFloat(t, m.WriteUtilizationPercent, "WriteUtilizationPercent")
		assertStorageValidFloat(t, m.ReadBytesPerSecond, "ReadBytesPerSecond")
		assertStorageValidFloat(t, m.WriteBytesPerSecond, "WriteBytesPerSecond")
		assertStorageValidFloat(t, m.ReadWriteBytesPerSecond, "ReadWriteBytesPerSecond")
		assertStorageValidFloat(t, m.ReadIOPerSecond, "ReadIOPerSecond")
		assertStorageValidFloat(t, m.WriteIOPerSecond, "WriteIOPerSecond")
	}
}

func assertStorageValidFloat(t *testing.T, value float64, name string) {
	assert.False(t, math.IsNaN(value), "%s should not be NaN", name)
	assert.False(t, math.IsInf(value, 0), "%s should not be Inf", name)
}
