//go:build integration
// +build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hostlink/domain/credential"
	"hostlink/internal/pgmetrics"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/network"
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
		"postgres:18-alpine",
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

	collector := pgmetrics.New()
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

	// Standalone primary has no replication_connected
	assert.Nil(t, metrics.ReplicationConnected, "standalone primary has no replication_connected")
}

func TestCollector_Collect_InvalidCredentials(t *testing.T) {
	_, cred := setupPostgresContainer(t)

	// Use wrong credentials
	invalidCred := cred
	wrongPass := "wrongpassword"
	invalidCred.Password = &wrongPass

	collector := pgmetrics.New()
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

	collector := pgmetrics.New()
	metrics, err := collector.Collect(cred)

	require.NoError(t, err)

	// After running queries, cache hit ratio should be meaningful
	// It should be high since we're reading the same data multiple times
	assert.Greater(t, metrics.CacheHitRatio, 0.0,
		"cache hit ratio should be greater than 0 after queries")
}

func TestCollector_Collect_DeltaBasedTPS(t *testing.T) {
	_, cred := setupPostgresContainer(t)

	// Generate some initial activity to ensure stats_reset is populated
	connStr := fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		cred.Host, cred.Port, cred.Username, *cred.Password, cred.Database)
	db, err := sql.Open("postgres", connStr)
	require.NoError(t, err)
	defer db.Close()

	ctx := context.Background()
	_, err = db.ExecContext(ctx, "SELECT 1")
	require.NoError(t, err)

	collector := pgmetrics.New()

	// First collection establishes baseline, returns 0 for rate metrics
	metrics1, err := collector.Collect(cred)
	require.NoError(t, err)
	assert.Equal(t, 0.0, metrics1.TransactionsPerSecond, "first collection should return 0 TPS")
	assert.Equal(t, 0.0, metrics1.CommittedTxPerSecond, "first collection should return 0 committed TPS")

	// Generate transactions between collections
	for range 100 {
		_, err = db.ExecContext(ctx, "SELECT 1")
		require.NoError(t, err)
	}

	// PostgreSQL stats flush at minimum 1-second intervals
	time.Sleep(1 * time.Second)

	// Second collection calculates delta-based TPS
	metrics2, err := collector.Collect(cred)
	require.NoError(t, err)
	assert.Greater(t, metrics2.TransactionsPerSecond, 0.0, "second collection should have TPS > 0")
	assert.Greater(t, metrics2.CommittedTxPerSecond, 0.0, "second collection should have committed TPS > 0")
}

func setupReplicationPair(t *testing.T) (primaryCred, replicaCred credential.Credential) {
	ctx := context.Background()

	net, err := network.New(ctx)
	require.NoError(t, err)

	hbaPath, err := filepath.Abs("testdata/primary-pg_hba.conf")
	require.NoError(t, err)

	primaryContainer, err := postgres.Run(ctx,
		"postgres:18-alpine",
		postgres.WithDatabase(testDatabase),
		postgres.WithUsername(testUser),
		postgres.WithPassword(testPassword),
		testcontainers.WithCmdArgs(
			"-c", "wal_level=replica",
			"-c", "max_wal_senders=4",
			"-c", "wal_keep_size=64MB",
			"-c", "hba_file=/etc/postgresql/pg_hba.conf",
		),
		testcontainers.WithFiles(testcontainers.ContainerFile{
			HostFilePath:      hbaPath,
			ContainerFilePath: "/etc/postgresql/pg_hba.conf",
			FileMode:          0644,
		}),
		network.WithNetwork([]string{"primary"}, net),
		postgres.BasicWaitStrategies(),
	)
	require.NoError(t, err)

	replicaScript := `
rm -rf /var/lib/postgresql/replica/*
until pg_basebackup -h primary -U testuser -D /var/lib/postgresql/replica -R -P --wal-method=stream; do
  sleep 1
done
exec postgres -D /var/lib/postgresql/replica
`

	replicaContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:      "postgres:18-alpine",
			User:       "postgres",
			Entrypoint: []string{"sh", "-c"},
			Cmd:        []string{replicaScript},
			Env: map[string]string{
				"PGDATA": "/var/lib/postgresql/replica",
			},
			ExposedPorts: []string{"5432/tcp"},
			Networks:     []string{net.Name},
			NetworkAliases: map[string][]string{
				net.Name: {"replica"},
			},
			WaitingFor: wait.ForLog("database system is ready to accept read-only connections").
				WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)

	t.Cleanup(func() {
		_ = replicaContainer.Terminate(ctx)
		_ = primaryContainer.Terminate(ctx)
		_ = net.Remove(ctx)
	})

	primaryHost, err := primaryContainer.Host(ctx)
	require.NoError(t, err)
	primaryPort, err := primaryContainer.MappedPort(ctx, "5432")
	require.NoError(t, err)

	replicaHost, err := replicaContainer.Host(ctx)
	require.NoError(t, err)
	replicaPort, err := replicaContainer.MappedPort(ctx, "5432")
	require.NoError(t, err)

	pw := testPassword
	primaryCred = credential.Credential{
		Host:     primaryHost,
		Port:     primaryPort.Int(),
		Username: testUser,
		Password: &pw,
		Database: testDatabase,
	}

	pw2 := testPassword
	replicaCred = credential.Credential{
		Host:     replicaHost,
		Port:     replicaPort.Int(),
		Username: testUser,
		Password: &pw2,
		Database: testDatabase,
	}

	return primaryCred, replicaCred
}

func TestCollector_Collect_Replica(t *testing.T) {
	primaryCred, replicaCred := setupReplicationPair(t)

	collector := pgmetrics.New()

	// Replica with active streaming: ReplicationConnected=true, ReplicationLagSeconds>=0
	replicaMetrics, err := collector.Collect(replicaCred)
	require.NoError(t, err)
	require.NotNil(t, replicaMetrics.ReplicationConnected, "replica should report replication_connected")
	assert.True(t, *replicaMetrics.ReplicationConnected, "replica should be connected to primary")
	assert.GreaterOrEqual(t, replicaMetrics.ReplicationLagSeconds, 0, "replica lag should be >= 0")

	// Primary with a connected replica: ReplicationConnected=nil, ReplicationLagSeconds>=0
	primaryMetrics, err := collector.Collect(primaryCred)
	require.NoError(t, err)
	assert.Nil(t, primaryMetrics.ReplicationConnected, "primary should not report replication_connected")
	assert.GreaterOrEqual(t, primaryMetrics.ReplicationLagSeconds, 0, "primary lag should be >= 0")
}

func BenchmarkCollector_Collect(b *testing.B) {
	_, cred := setupPostgresContainer(&testing.T{})
	setupTestData(&testing.T{}, cred)

	collector := pgmetrics.New()

	b.ResetTimer()
	for b.Loop() {
		_, err := collector.Collect(cred)
		if err != nil {
			b.Fatalf("collection failed: %v", err)
		}
	}
}
