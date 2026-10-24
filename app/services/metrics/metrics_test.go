package metrics

import (
	"context"
	"errors"
	"testing"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestPush_NoAgentID(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}

	mocks.agentstate.On("GetAgentID").Return("")

	err := mp.Push(testCred)

	assert.EqualError(t, err, "agent not registered: missing agent ID")
	mocks.agentstate.AssertExpectations(t)
	mocks.collector.AssertNotCalled(t, "Collect")
	mocks.apiserver.AssertNotCalled(t, "PushMetrics")
}

// Push Tests - Partial Collection (system fails, db succeeds)
func TestPush_SystemMetricsFailure_StillPushesDbMetrics(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.syscollector.On("Collect", mock.Anything).
		Return(domainmetrics.SystemMetrics{}, errors.New("collection failed"))
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true, ConnectionsTotal: 5}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		hasNetwork := false
		hasDb := false
		hasStorage := false
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypeNetwork {
				hasNetwork = true
			}
			if ms.Type == domainmetrics.MetricTypePostgreSQLDatabase {
				dbMetrics := ms.Metrics.(domainmetrics.PostgreSQLDatabaseMetrics)
				hasDb = dbMetrics.Up
			}
			if ms.Type == domainmetrics.MetricTypeStorage {
				hasStorage = true
			}
		}
		return hasNetwork && hasDb && hasStorage
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.collector.AssertExpectations(t)
	mocks.apiserver.AssertExpectations(t)
}

// Push Tests - Partial Collection (system succeeds, db fails)
func TestPush_DatabaseMetricsFailure_StillPushesSystemMetricsAndDbWithUpFalse(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}
	collectErr := errors.New("connection refused")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{}, collectErr)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		hasSys := false
		hasNetwork := false
		hasStorage := false
		hasDbWithUpFalse := false
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypeSystem {
				hasSys = true
			}
			if ms.Type == domainmetrics.MetricTypeNetwork {
				hasNetwork = true
			}
			if ms.Type == domainmetrics.MetricTypeStorage {
				hasStorage = true
			}
			if ms.Type == domainmetrics.MetricTypePostgreSQLDatabase {
				dbMetrics := ms.Metrics.(domainmetrics.PostgreSQLDatabaseMetrics)
				hasDbWithUpFalse = !dbMetrics.Up
			}
		}
		return hasSys && hasNetwork && hasStorage && hasDbWithUpFalse
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.collector.AssertExpectations(t)
	mocks.apiserver.AssertExpectations(t)
}

// Push Tests - All Collections Fail — still pushes postgresql.database with up=false
func TestPush_AllCollectionsFail_StillPushesDbWithUpFalse(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.syscollector.On("Collect", mock.Anything).
		Return(domainmetrics.SystemMetrics{}, errors.New("collection failed"))
	mocks.netcollector.On("Collect", mock.Anything).
		Return(domainmetrics.NetworkMetrics{}, errors.New("network failed"))
	mocks.storagecollector.On("Collect", mock.Anything).
		Return(nil, errors.New("storage failed"))
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{}, errors.New("connection refused"))
	mocks.pgbouncercollector.On("Collect", testCred).
		Return(domainmetrics.PgBouncerMetrics{}, errors.New("pgbouncer not running"))
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		// Expect 2 metric sets: postgresql.database (up=false) + pgbouncer.stats (up=false)
		if len(p.MetricSets) != 2 {
			return false
		}
		hasDbUpFalse := false
		hasPgBouncerUpFalse := false
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypePostgreSQLDatabase {
				dbMetrics := ms.Metrics.(domainmetrics.PostgreSQLDatabaseMetrics)
				hasDbUpFalse = !dbMetrics.Up
			}
			if ms.Type == domainmetrics.MetricTypePgBouncer {
				pbMetrics := ms.Metrics.(domainmetrics.PgBouncerMetrics)
				hasPgBouncerUpFalse = !pbMetrics.Up
			}
		}
		return hasDbUpFalse && hasPgBouncerUpFalse
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}

func TestPush_APIServerPushFailure(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}
	pushErr := errors.New("network timeout")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true, ConnectionsTotal: 5}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.Anything).
		Return(pushErr)

	err := mp.Push(testCred)

	assert.Equal(t, pushErr, err)
	mocks.apiserver.AssertExpectations(t)
}

func TestPush_Success_ValidatesPayloadSchema(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{
		Host:          "localhost",
		Port:          5432,
		Username:      "postgres",
		DataDirectory: "/var/lib/postgresql",
	}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	connected := true
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{
			Up:                    true,
			ConnectionsTotal:      10,
			MaxConnections:        100,
			CacheHitRatio:         95.5,
			TransactionsPerSecond: 100.0,
			CommittedTxPerSecond:  98.0,
			BlocksReadPerSecond:   5.5,
			ReplicationLagSeconds: 0,
			ReplicationConnected:  &connected,
		}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		// Validate payload structure
		if p.Version != "1.0" {
			return false
		}
		if p.TimestampMs <= 0 {
			return false
		}
		if p.Resource.AgentID != "agent-123" {
			return false
		}
		if p.Resource.HostName == "" {
			return false
		}
		if len(p.MetricSets) != 5 {
			return false
		}

		// Validate metrics
		var sysMetrics domainmetrics.SystemMetrics
		var netMetrics domainmetrics.NetworkMetrics
		var dbMetrics domainmetrics.PostgreSQLDatabaseMetrics
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypeSystem {
				sysMetrics = ms.Metrics.(domainmetrics.SystemMetrics)
			}
			if ms.Type == domainmetrics.MetricTypeNetwork {
				netMetrics = ms.Metrics.(domainmetrics.NetworkMetrics)
			}
			if ms.Type == domainmetrics.MetricTypePostgreSQLDatabase {
				dbMetrics = ms.Metrics.(domainmetrics.PostgreSQLDatabaseMetrics)
			}
		}

		// Check system metrics values
		if sysMetrics.CPUPercent != 25.0 {
			return false
		}
		if sysMetrics.MemoryPercent != 50.0 {
			return false
		}
		if sysMetrics.LoadAvg1 != 1.5 {
			return false
		}
		if sysMetrics.LoadAvg5 != 2.0 {
			return false
		}
		if sysMetrics.LoadAvg15 != 2.5 {
			return false
		}
		if sysMetrics.SwapUsagePercent != 10.0 {
			return false
		}
		if sysMetrics.DiskUsagePercent != 75.0 {
			return false
		}

		// Check network metrics values
		if netMetrics.RecvBytesPerSec != 1000.0 {
			return false
		}
		if netMetrics.SentBytesPerSec != 500.0 {
			return false
		}

		// Check database metrics values
		if !dbMetrics.Up {
			return false
		}
		if dbMetrics.ConnectionsTotal != 10 {
			return false
		}
		if dbMetrics.MaxConnections != 100 {
			return false
		}
		if dbMetrics.CacheHitRatio != 95.5 {
			return false
		}
		if dbMetrics.TransactionsPerSecond != 100.0 {
			return false
		}
		if dbMetrics.CommittedTxPerSecond != 98.0 {
			return false
		}
		if dbMetrics.BlocksReadPerSecond != 5.5 {
			return false
		}
		if dbMetrics.ReplicationLagSeconds != 0 {
			return false
		}
		if dbMetrics.ReplicationConnected == nil || *dbMetrics.ReplicationConnected != true {
			return false
		}

		return true
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.agentstate.AssertExpectations(t)
	mocks.collector.AssertExpectations(t)
	mocks.apiserver.AssertExpectations(t)
}

func TestPush_ContextPropagation(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", Port: 5432, DataDirectory: "/data"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true}, nil)
	mocks.apiserver.On("PushMetrics", mock.MatchedBy(func(ctx context.Context) bool {
		return ctx != nil
	}), mock.Anything).
		Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}

// Edge case tests
func TestPush_CredentialPassedCorrectly(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{
		Host:          "db.example.com",
		Port:          5432,
		Username:      "admin",
		Dialect:       "postgresql",
		DataDirectory: "/var/lib/postgresql",
	}

	mocks.agentstate.On("GetAgentID").Return("agent-456")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", mock.MatchedBy(func(c credential.Credential) bool {
		return c.Host == testCred.Host &&
			c.Port == testCred.Port &&
			c.Username == testCred.Username &&
			c.Dialect == testCred.Dialect
	})).Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.Anything).
		Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.collector.AssertExpectations(t)
}

func TestPush_DatabaseDown_SendsUpFalseWithZeroMetrics(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{}, errors.New("connection refused"))
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypePostgreSQLDatabase {
				dbMetrics := ms.Metrics.(domainmetrics.PostgreSQLDatabaseMetrics)
				// up must be false
				if dbMetrics.Up {
					return false
				}
				// all other fields must be zero
				if dbMetrics.ConnectionsTotal != 0 {
					return false
				}
				if dbMetrics.MaxConnections != 0 {
					return false
				}
				if dbMetrics.CacheHitRatio != 0 {
					return false
				}
				if dbMetrics.TransactionsPerSecond != 0 {
					return false
				}
				if dbMetrics.CommittedTxPerSecond != 0 {
					return false
				}
				if dbMetrics.BlocksReadPerSecond != 0 {
					return false
				}
				if dbMetrics.ReplicationLagSeconds != 0 {
					return false
				}
				if dbMetrics.ReplicationConnected != nil {
					return false
				}
				return true
			}
		}
		return false // postgresql.database metric set must be present
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}

// Verifies storage metrics are included in the payload

// A ClickHouse node the agent could not query says nothing about its topology:
// the verdict fields read "unknown", never an empty string or not_applicable.
func TestPush_ClickHouseDown_TopologyUnknown(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", Port: 9000, Dialect: "clickhouse"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	mocks.clickhousecollector.On("Collect", testCred).
		Return(domainmetrics.ClickHouseDatabaseMetrics{}, errors.New("ping: connection refused"))

	var got domainmetrics.ClickHouseDatabaseMetrics
	found := false
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypeClickHouseDatabase {
				got = ms.Metrics.(domainmetrics.ClickHouseDatabaseMetrics)
				found = true
			}
		}
		return found
	})).Return(nil)

	assert.NoError(t, mp.Push(testCred))
	assert.True(t, found, "clickhouse.database metric set must be present")
	assert.False(t, got.Up)
	assert.False(t, got.KeeperConnected, "legacy keeper_connected keeps today's value")
	assert.Equal(t, domainmetrics.ClickHouseModeUnknown, got.Mode)
	assert.Equal(t, domainmetrics.ClickHouseKeeperUnknown, got.KeeperStatus)
	assert.Equal(t, domainmetrics.ClickHouseReplicationUnknown, got.ReplicationStatus)
	assert.Nil(t, got.ReplicatedTablesCount)
	assert.Nil(t, got.BrokenDetachedPartsCount)
}
