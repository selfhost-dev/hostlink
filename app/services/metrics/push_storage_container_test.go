package metrics

import (
	"errors"
	"testing"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/containermetrics"
	"hostlink/internal/storagemetrics"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestPush_IncludesStorageMetrics(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", Port: 5432, DataDirectory: "/data"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		hasStorage := false
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypeStorage {
				hasStorage = true
			}
		}
		return hasStorage
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.storagecollector.AssertExpectations(t)
	mocks.apiserver.AssertExpectations(t)
}

// Verifies when storage collection fails, other metrics still pushed
func TestPush_StorageMetricsFailure_StillPushesOtherMetrics(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", Port: 5432, DataDirectory: "/data"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	mocks.storagecollector.On("Collect", mock.Anything).
		Return(nil, errors.New("storage collection failed"))
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		hasSys := false
		hasNet := false
		hasDb := false
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypeSystem {
				hasSys = true
			}
			if ms.Type == domainmetrics.MetricTypeNetwork {
				hasNet = true
			}
			if ms.Type == domainmetrics.MetricTypePostgreSQLDatabase {
				hasDb = true
			}
		}
		return hasSys && hasNet && hasDb
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}

// Verifies each mount becomes a separate MetricSet
func TestPush_StorageMetricsMultipleMounts(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", Port: 5432, DataDirectory: "/data"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	mocks.storagecollector.On("Collect", mock.Anything).Return([]storagemetrics.StorageMetricSet{
		{
			Attributes: domainmetrics.StorageAttributes{MountPoint: "/", Device: "/dev/sda1"},
			Metrics:    domainmetrics.StorageMetrics{DiskUsedPercent: 50.0},
		},
		{
			Attributes: domainmetrics.StorageAttributes{MountPoint: "/home", Device: "/dev/sda2"},
			Metrics:    domainmetrics.StorageMetrics{DiskUsedPercent: 75.0},
		},
	}, nil)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		storageCount := 0
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypeStorage {
				storageCount++
			}
		}
		return storageCount == 2
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}

// Verifies attributes are set correctly
func TestPush_ContainerMetrics_IncludesCoolifyName(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", Port: 5432}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true}, nil)
	mocks.storagecollector.On("Collect", mock.Anything).Return([]storagemetrics.StorageMetricSet(nil), nil)
	// Clear the default nil-returning stub so the custom return below takes effect.
	mocks.containercollector.ExpectedCalls = nil
	mocks.containercollector.On("Collect", mock.Anything).Return([]containermetrics.ContainerMetricSet{
		{
			Attributes: domainmetrics.ContainerAttributes{
				ContainerID:   "abc123def456",
				ContainerName: "myapp-abc1",
				Image:         "myapp:latest",
				CoolifyAppID:  "app-uuid-001",
				CoolifyType:   "application",
				CoolifyName:   "myapp",
			},
			Metrics: domainmetrics.ContainerMetrics{Up: true, CPUPercent: 5.0},
		},
	}, nil)

	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		for _, ms := range p.MetricSets {
			if ms.Type != domainmetrics.MetricTypeContainer {
				continue
			}
			attrs := ms.Attributes
			return attrs["coolify_name"] == "myapp" &&
				attrs["coolify_app_id"] == "app-uuid-001" &&
				attrs["coolify_type"] == "application" &&
				attrs["container_name"] == "myapp-abc1"
		}
		return false
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}

func TestPush_ContainerMetrics_IncludesCoolifyServiceID(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", Port: 5432}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true}, nil)
	mocks.storagecollector.On("Collect", mock.Anything).Return([]storagemetrics.StorageMetricSet(nil), nil)
	mocks.containercollector.ExpectedCalls = nil
	// A Coolify SERVICE container (e.g. Twenty's `worker`): the human names don't
	// contain the service uuid, but coolify_service_id carries it so the control
	// plane can attribute the metric to the CoolifyService.
	mocks.containercollector.On("Collect", mock.Anything).Return([]containermetrics.ContainerMetricSet{
		{
			Attributes: domainmetrics.ContainerAttributes{
				ContainerID:      "def456abc123",
				ContainerName:    "worker",
				Image:            "twentycrm/twenty:v1.15",
				CoolifyType:      "service",
				CoolifyName:      "worker",
				CoolifyServiceID: "svcuuid789",
			},
			Metrics: domainmetrics.ContainerMetrics{Up: true, CPUPercent: 3.0},
		},
	}, nil)

	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		for _, ms := range p.MetricSets {
			if ms.Type != domainmetrics.MetricTypeContainer {
				continue
			}
			return ms.Attributes["coolify_service_id"] == "svcuuid789" &&
				ms.Attributes["container_name"] == "worker"
		}
		return false
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}

func TestPush_StorageMetricsWithAttributes(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", Port: 5432, DataDirectory: "/data"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	mocks.storagecollector.On("Collect", mock.Anything).Return([]storagemetrics.StorageMetricSet{
		{
			Attributes: domainmetrics.StorageAttributes{
				MountPoint:     "/data",
				Device:         "/dev/nvme0n1p1",
				FilesystemType: "xfs",
				IsReadOnly:     true,
			},
			Metrics: domainmetrics.StorageMetrics{DiskUsedPercent: 80.0},
		},
	}, nil)
	setupPgBouncerCollectorMocks(mocks.pgbouncercollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{Up: true}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypeStorage {
				attrs := ms.Attributes
				if attrs["mount_point"] != "/data" {
					return false
				}
				if attrs["device"] != "/dev/nvme0n1p1" {
					return false
				}
				if attrs["filesystem_type"] != "xfs" {
					return false
				}
				if attrs["is_read_only"] != true {
					return false
				}
				return true
			}
		}
		return false
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}
