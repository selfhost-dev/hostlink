package metrics

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/containermetrics"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/storagemetrics"
	"hostlink/internal/traefikmetrics"

	"github.com/stretchr/testify/mock"
)

var testPrivateKey, _ = rsa.GenerateKey(rand.Reader, 512)

// Mock implementations
type MockAPIServer struct {
	mock.Mock
}

func (m *MockAPIServer) GetMetricsCreds(ctx context.Context, agentID string) ([]credential.Credential, error) {
	args := m.Called(ctx, agentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]credential.Credential), args.Error(1)
}

func (m *MockAPIServer) PushMetrics(ctx context.Context, payload domainmetrics.MetricPayload) error {
	args := m.Called(ctx, payload)
	return args.Error(0)
}

type MockAgentState struct {
	mock.Mock
}

func (m *MockAgentState) Save() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAgentState) Load() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockAgentState) GetAgentID() string {
	args := m.Called()
	return args.String(0)
}

func (m *MockAgentState) SetAgentID(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockAgentState) Clear() error {
	args := m.Called()
	return args.Error(0)
}

type MockCollector struct {
	mock.Mock
}

func (m *MockCollector) Collect(cred credential.Credential) (domainmetrics.PostgreSQLDatabaseMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(domainmetrics.PostgreSQLDatabaseMetrics), args.Error(1)
}

type MockSysCollector struct {
	mock.Mock
}

func (m *MockSysCollector) Collect(ctx context.Context) (domainmetrics.SystemMetrics, error) {
	args := m.Called(ctx)
	return args.Get(0).(domainmetrics.SystemMetrics), args.Error(1)
}

type MockNetCollector struct {
	mock.Mock
}

func (m *MockNetCollector) Collect(ctx context.Context) (domainmetrics.NetworkMetrics, error) {
	args := m.Called(ctx)
	return args.Get(0).(domainmetrics.NetworkMetrics), args.Error(1)
}

type MockStorageCollector struct {
	mock.Mock
}

func (m *MockStorageCollector) Collect(ctx context.Context) ([]storagemetrics.StorageMetricSet, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]storagemetrics.StorageMetricSet), args.Error(1)
}

type MockCrypto struct {
	mock.Mock
}

func (m *MockCrypto) GenerateKeypair(bits int) (*rsa.PrivateKey, error) {
	args := m.Called(bits)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rsa.PrivateKey), args.Error(1)
}

func (m *MockCrypto) LoadOrGenerateKeypair(keyPath string, bits int) (*rsa.PrivateKey, error) {
	args := m.Called(keyPath, bits)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rsa.PrivateKey), args.Error(1)
}

func (m *MockCrypto) SavePrivateKey(privateKey *rsa.PrivateKey, keyPath string) error {
	args := m.Called(privateKey, keyPath)
	return args.Error(0)
}

func (m *MockCrypto) LoadPrivateKey(keyPath string) (*rsa.PrivateKey, error) {
	args := m.Called(keyPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rsa.PrivateKey), args.Error(1)
}

func (m *MockCrypto) LoadPublicKey(keyPath string) (*rsa.PublicKey, error) {
	args := m.Called(keyPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rsa.PublicKey), args.Error(1)
}

func (m *MockCrypto) GetPublicKeyBase64(privateKey *rsa.PrivateKey) (string, error) {
	args := m.Called(privateKey)
	return args.String(0), args.Error(1)
}

func (m *MockCrypto) GetPublicKeyPEM(privateKey *rsa.PrivateKey) (string, error) {
	args := m.Called(privateKey)
	return args.String(0), args.Error(1)
}

func (m *MockCrypto) ParsePublicKeyFromBase64(base64String string) (*rsa.PublicKey, error) {
	args := m.Called(base64String)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rsa.PublicKey), args.Error(1)
}

func (m *MockCrypto) ParsePublicKeyFromPEM(pemString string) (*rsa.PublicKey, error) {
	args := m.Called(pemString)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*rsa.PublicKey), args.Error(1)
}

func (m *MockCrypto) EncryptWithPublicKey(msg string, pub *rsa.PublicKey) (string, error) {
	args := m.Called(msg, pub)
	return args.String(0), args.Error(1)
}

func (m *MockCrypto) DecryptWithPrivateKey(ciphertextBase64 string, privateKey *rsa.PrivateKey) (string, error) {
	args := m.Called(ciphertextBase64, privateKey)
	return args.String(0), args.Error(1)
}

type MockPgBouncerCollector struct {
	mock.Mock
}

func (m *MockPgBouncerCollector) Collect(cred credential.Credential) (domainmetrics.PgBouncerMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(domainmetrics.PgBouncerMetrics), args.Error(1)
}

type MockMySQLCollector struct {
	mock.Mock
}

func (m *MockMySQLCollector) Collect(cred credential.Credential) (domainmetrics.MySQLDatabaseMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(domainmetrics.MySQLDatabaseMetrics), args.Error(1)
}

type MockMongoDBCollector struct {
	mock.Mock
}

func (m *MockMongoDBCollector) Collect(cred credential.Credential) (domainmetrics.MongoDBMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(domainmetrics.MongoDBMetrics), args.Error(1)
}

type MockRedisCollector struct {
	mock.Mock
}

func (m *MockRedisCollector) Collect(cred credential.Credential) (domainmetrics.RedisMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(domainmetrics.RedisMetrics), args.Error(1)
}

type MockClickHouseCollector struct {
	mock.Mock
}

func (m *MockClickHouseCollector) Collect(cred credential.Credential) (domainmetrics.ClickHouseDatabaseMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(domainmetrics.ClickHouseDatabaseMetrics), args.Error(1)
}

type MockOpenSearchCollector struct {
	mock.Mock
}

func (m *MockOpenSearchCollector) Collect(cred credential.Credential) (domainmetrics.OpenSearchDatabaseMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(domainmetrics.OpenSearchDatabaseMetrics), args.Error(1)
}

type MockKafkaCollector struct {
	mock.Mock
}

func (m *MockKafkaCollector) Collect(cred credential.Credential) (domainmetrics.KafkaDatabaseMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(domainmetrics.KafkaDatabaseMetrics), args.Error(1)
}

type MockContainerCollector struct {
	mock.Mock
}

func (m *MockContainerCollector) Collect(ctx context.Context) ([]containermetrics.ContainerMetricSet, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]containermetrics.ContainerMetricSet), args.Error(1)
}

type MockTraefikCollector struct {
	mock.Mock
}

func (m *MockTraefikCollector) Collect(ctx context.Context) ([]traefikmetrics.EntrypointMetricSet, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]traefikmetrics.EntrypointMetricSet), args.Error(1)
}

func (m *MockTraefikCollector) CollectRouters(ctx context.Context) ([]traefikmetrics.RouterMetricSet, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]traefikmetrics.RouterMetricSet), args.Error(1)
}

type MockDockerDiscoverer struct {
	mock.Mock
}

func (m *MockDockerDiscoverer) DiscoverDatabases(ctx context.Context) ([]dockerdiscovery.DiscoveredDatabase, error) {
	args := m.Called(ctx)
	return args.Get(0).([]dockerdiscovery.DiscoveredDatabase), args.Error(1)
}

// Test helpers
type testMocks struct {
	apiserver           *MockAPIServer
	agentstate          *MockAgentState
	collector           *MockCollector
	syscollector        *MockSysCollector
	netcollector        *MockNetCollector
	storagecollector    *MockStorageCollector
	pgbouncercollector  *MockPgBouncerCollector
	mysqlcollector      *MockMySQLCollector
	mongodbcollector    *MockMongoDBCollector
	rediscollector      *MockRedisCollector
	clickhousecollector *MockClickHouseCollector
	opensearchcollector *MockOpenSearchCollector
	kafkacollector      *MockKafkaCollector
	containercollector  *MockContainerCollector
	traefikcollector    *MockTraefikCollector
	dockerDiscoverer    *MockDockerDiscoverer
	crypto              *MockCrypto
}

func setupTestMetricsPusher() (*metricspusher, *testMocks) {
	mocks := &testMocks{
		apiserver:           new(MockAPIServer),
		agentstate:          new(MockAgentState),
		collector:           new(MockCollector),
		syscollector:        new(MockSysCollector),
		netcollector:        new(MockNetCollector),
		storagecollector:    new(MockStorageCollector),
		pgbouncercollector:  new(MockPgBouncerCollector),
		mysqlcollector:      new(MockMySQLCollector),
		mongodbcollector:    new(MockMongoDBCollector),
		rediscollector:      new(MockRedisCollector),
		clickhousecollector: new(MockClickHouseCollector),
		opensearchcollector: new(MockOpenSearchCollector),
		kafkacollector:      new(MockKafkaCollector),
		containercollector:  new(MockContainerCollector),
		traefikcollector:    new(MockTraefikCollector),
		dockerDiscoverer:    new(MockDockerDiscoverer),
		crypto:              new(MockCrypto),
	}

	mp := NewWithDependencies(
		mocks.apiserver,
		mocks.agentstate,
		mocks.collector,
		mocks.syscollector,
		mocks.netcollector,
		mocks.storagecollector,
		mocks.pgbouncercollector,
		mocks.mysqlcollector,
		mocks.mongodbcollector,
		mocks.rediscollector,
		mocks.clickhousecollector,
		mocks.opensearchcollector,
		mocks.kafkacollector,
		mocks.containercollector,
		mocks.traefikcollector,
		mocks.dockerDiscoverer,
		mocks.crypto,
		"/test/key/path",
	)

	// Default: no Docker containers discovered
	mocks.dockerDiscoverer.On("DiscoverDatabases", mock.Anything).
		Return([]dockerdiscovery.DiscoveredDatabase{}, nil)
	// Default: no running containers or Traefik services (no metric sets added)
	mocks.containercollector.On("Collect", mock.Anything).
		Return([]containermetrics.ContainerMetricSet(nil), nil)
	mocks.traefikcollector.On("Collect", mock.Anything).
		Return([]traefikmetrics.EntrypointMetricSet(nil), nil)
	mocks.traefikcollector.On("CollectRouters", mock.Anything).
		Return([]traefikmetrics.RouterMetricSet(nil), nil)

	return mp, mocks
}

// Constructor Tests
func setupSysCollectorMocks(collector *MockSysCollector) {
	collector.On("Collect", mock.Anything).Return(domainmetrics.SystemMetrics{
		CPUPercent:       25.0,
		CPUIOWaitPercent: 5.0,
		MemoryPercent:    50.0,
		LoadAvg1:         1.5,
		LoadAvg5:         2.0,
		LoadAvg15:        2.5,
		SwapUsagePercent: 10.0,
		DiskUsagePercent: 75.0,
	}, nil)
}

func setupNetCollectorMocks(collector *MockNetCollector) {
	collector.On("Collect", mock.Anything).Return(domainmetrics.NetworkMetrics{
		RecvBytesPerSec: 1000.0,
		SentBytesPerSec: 500.0,
	}, nil)
}

func setupPgBouncerCollectorMocks(collector *MockPgBouncerCollector) {
	collector.On("Collect", mock.Anything).
		Return(domainmetrics.PgBouncerMetrics{}, errors.New("connection refused: pgbouncer not running"))
}

func setupStorageCollectorMocks(collector *MockStorageCollector) {
	collector.On("Collect", mock.Anything).Return([]storagemetrics.StorageMetricSet{
		{
			Attributes: domainmetrics.StorageAttributes{
				MountPoint:     "/",
				Device:         "/dev/sda1",
				FilesystemType: "ext4",
				IsReadOnly:     false,
			},
			Metrics: domainmetrics.StorageMetrics{
				DiskTotalBytes:          100000000000,
				DiskUsedBytes:           60000000000,
				DiskFreeBytes:           40000000000,
				DiskUsedPercent:         60.0,
				DiskFreePercent:         40.0,
				TotalUtilizationPercent: 25.0,
			},
		},
	}, nil)
}

// Verifies database down sends up=false with zero metrics
