//go:build integration
// +build integration

package pgmetrics

import (
	"context"
	"database/sql"
	"fmt"
	"hostlink/domain/credential"
	"strings"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	testUser     = "testuser"
	testPassword = "testpass"
	testDatabase = "testdb"
)

func setupPostgresContainer(t *testing.T) (*postgres.PostgresContainer, credential.Credential) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase(testDatabase),
		postgres.WithUsername(testUser),
		postgres.WithPassword(testPassword),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		if err := pgContainer.Terminate(ctx); err != nil {
			t.Logf("failed to terminate container: %v", err)
		}
	})

	host, err := pgContainer.Host(ctx)
	require.NoError(t, err)

	port, err := pgContainer.MappedPort(ctx, "5432")
	require.NoError(t, err)

	testPasswordPtr := testPassword
	cred := credential.Credential{
		Host:     host,
		Port:     port.Int(),
		Username: testUser,
		Password: &testPasswordPtr,
		Database: testDatabase,
	}

	return pgContainer, cred
}

func setupTestData(t *testing.T, cred credential.Credential) {
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cred.Host, cred.Port, cred.Username, *cred.Password, cred.Database)

	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS test_table (
			id SERIAL PRIMARY KEY,
			data TEXT
		);
	`)
	require.NoError(t, err)

	for i := range 100 {
		_, err = db.ExecContext(ctx, "INSERT INTO test_table (data) VALUES ($1)", fmt.Sprintf("test-data-%d", i))
		require.NoError(t, err)
	}

	for range 50 {
		var count int
		err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM test_table").Scan(&count)
		require.NoError(t, err)
	}
}

func TestCollector_Collect(t *testing.T) {
	_, cred := setupPostgresContainer(t)
	setupTestData(t, cred)

	collector := New()
	metrics, err := collector.Collect(cred)

	require.NoError(t, err)

	// Verify connections are being tracked
	assert.GreaterOrEqual(t, metrics.ConnectionsTotal, 1, "should have at least 1 connection")

	// Verify max connections is set (default is typically 100)
	assert.Greater(t, metrics.MaxConnections, 0, "max_connections should be greater than 0")

	// Verify cache hit ratio is calculated (should be between 0 and 100)
	assert.GreaterOrEqual(t, metrics.CacheHitRatio, 0.0)
	assert.LessOrEqual(t, metrics.CacheHitRatio, 100.0)

	// TPS might be 0 for a new database without stats_reset or very low activity
	assert.GreaterOrEqual(t, metrics.TransactionsPerSecond, 0.0)

	// Committed TPS might be 0 for a new database without stats_reset
	assert.GreaterOrEqual(t, metrics.CommittedTxPerSecond, 0.0)

	// Blocks read per second might be 0 for a new database without stats_reset
	assert.GreaterOrEqual(t, metrics.BlocksReadPerSecond, 0.0)

	// Replication lag should be 0 when no replication is configured
	assert.Equal(t, 0, metrics.ReplicationLagSeconds)
}

func TestCollector_Collect_InvalidCredentials(t *testing.T) {
	_, cred := setupPostgresContainer(t)

	// Use wrong credentials
	invalidCred := cred
	wrongPass := "wrongpassword"
	invalidCred.Password = &wrongPass

	collector := New()
	_, err := collector.Collect(invalidCred)

	require.Error(t, err)
	// Just verify it's an authentication error, don't check exact message
	assert.True(t,
		strings.Contains(err.Error(), "authentication failed") ||
			strings.Contains(err.Error(), "ping failed"),
		"expected authentication or connection error, got: %v", err)
}

func TestCollector_Collect_CacheHitRatio(t *testing.T) {
	_, cred := setupPostgresContainer(t)
	setupTestData(t, cred)

	collector := New()
	metrics, err := collector.Collect(cred)

	require.NoError(t, err)

	// After running queries, cache hit ratio should be meaningful
	// It should be high since we're reading the same data multiple times
	assert.Greater(t, metrics.CacheHitRatio, 0.0,
		"cache hit ratio should be greater than 0 after queries")
}

func TestCollector_Collect_Timeout(t *testing.T) {
	// Create a collector with very short timeout
	collector := pgmetrics{
		queryTimeout: 1 * time.Nanosecond,
	}

	_, cred := setupPostgresContainer(t)

	_, err := collector.Collect(cred)

	// Should timeout or fail quickly
	require.Error(t, err)
}

func BenchmarkCollector_Collect(b *testing.B) {
	_, cred := setupPostgresContainer(&testing.T{})
	setupTestData(&testing.T{}, cred)

	collector := New()

	b.ResetTimer()
	for b.Loop() {
		_, err := collector.Collect(cred)
		if err != nil {
			b.Fatalf("collection failed: %v", err)
		}
	}
}
