package metrics

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/storagemetrics"

	"github.com/stretchr/testify/assert"
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

// Test helpers
type testMocks struct {
	apiserver        *MockAPIServer
	agentstate       *MockAgentState
	collector        *MockCollector
	syscollector     *MockSysCollector
	netcollector     *MockNetCollector
	storagecollector *MockStorageCollector
	crypto           *MockCrypto
}

func setupTestMetricsPusher() (*metricspusher, *testMocks) {
	mocks := &testMocks{
		apiserver:        new(MockAPIServer),
		agentstate:       new(MockAgentState),
		collector:        new(MockCollector),
		syscollector:     new(MockSysCollector),
		netcollector:     new(MockNetCollector),
		storagecollector: new(MockStorageCollector),
		crypto:           new(MockCrypto),
	}

	mp := NewWithDependencies(
		mocks.apiserver,
		mocks.agentstate,
		mocks.collector,
		mocks.syscollector,
		mocks.netcollector,
		mocks.storagecollector,
		mocks.crypto,
		"/test/key/path",
	)

	return mp, mocks
}

// Constructor Tests
func TestNewWithDependencies_AllFieldsSet(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()

	assert.NotNil(t, mp)
	assert.Equal(t, mocks.apiserver, mp.apiserver)
	assert.Equal(t, mocks.agentstate, mp.agentstate)
	assert.Equal(t, mocks.collector, mp.metricscollector)
	assert.Equal(t, mocks.syscollector, mp.syscollector)
	assert.Equal(t, mocks.netcollector, mp.netcollector)
	assert.Equal(t, mocks.crypto, mp.crypto)
	assert.Equal(t, "/test/key/path", mp.privateKeyPath)
}

// GetCreds Tests - Agent State Validation
func TestGetCreds_NoAgentID(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()

	mocks.agentstate.On("GetAgentID").Return("")

	creds, err := mp.GetCreds()

	assert.Nil(t, creds)
	assert.EqualError(t, err, "agent not registered: missing agent ID")
	mocks.agentstate.AssertExpectations(t)
	mocks.apiserver.AssertNotCalled(t, "GetMetricsCreds")
}

// GetCreds Tests - API Interaction
func TestGetCreds_APIServerError(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	expectedErr := errors.New("api connection failed")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").
		Return(nil, expectedErr)

	creds, err := mp.GetCreds()

	assert.Nil(t, creds)
	assert.Equal(t, expectedErr, err)
	mocks.agentstate.AssertExpectations(t)
	mocks.apiserver.AssertExpectations(t)
}

func TestGetCreds_EmptyCredentials(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").
		Return([]credential.Credential{}, nil)

	creds, err := mp.GetCreds()

	assert.NoError(t, err)
	assert.Empty(t, creds)
	mocks.crypto.AssertNotCalled(t, "LoadPrivateKey")
	mocks.crypto.AssertNotCalled(t, "DecryptWithPrivateKey")
}

// GetCreds Tests - Decryption Flow
func TestGetCreds_PrivateKeyLoadFailure(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	expectedErr := errors.New("key file not found")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").
		Return([]credential.Credential{
			{PasswdEnc: "encrypted-password"},
		}, nil)
	mocks.crypto.On("LoadPrivateKey", "/test/key/path").
		Return(nil, expectedErr)

	creds, err := mp.GetCreds()

	assert.Nil(t, creds)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load private key")
	assert.ErrorIs(t, err, expectedErr)
}

func TestGetCreds_DecryptionFailure(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	decryptErr := errors.New("decryption failed")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").
		Return([]credential.Credential{
			{PasswdEnc: "encrypted-password"},
		}, nil)
	mocks.crypto.On("LoadPrivateKey", "/test/key/path").
		Return(testPrivateKey, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "encrypted-password", testPrivateKey).
		Return("", decryptErr)

	creds, err := mp.GetCreds()

	assert.Nil(t, creds)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decrypt password for credential 0")
	assert.ErrorIs(t, err, decryptErr)
}

func TestGetCreds_SkipEmptyPasswdEnc(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	password2 := "decrypted-pass-2"

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").
		Return([]credential.Credential{
			{PasswdEnc: ""},
			{PasswdEnc: "encrypted-password-2"},
			{PasswdEnc: ""},
		}, nil)
	mocks.crypto.On("LoadPrivateKey", "/test/key/path").
		Return(testPrivateKey, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "encrypted-password-2", testPrivateKey).
		Return(password2, nil)

	creds, err := mp.GetCreds()

	assert.NoError(t, err)
	assert.Len(t, creds, 3)
	assert.Nil(t, creds[0].Password)
	assert.NotNil(t, creds[1].Password)
	assert.Equal(t, password2, *creds[1].Password)
	assert.Nil(t, creds[2].Password)

	// Verify decryption only called once
	mocks.crypto.AssertNumberOfCalls(t, "DecryptWithPrivateKey", 1)
}

func TestGetCreds_Success(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	decryptedPassword := "super-secret-password"

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").
		Return([]credential.Credential{
			{PasswdEnc: "encrypted-password"},
		}, nil)
	mocks.crypto.On("LoadPrivateKey", "/test/key/path").
		Return(testPrivateKey, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "encrypted-password", testPrivateKey).
		Return(decryptedPassword, nil)

	creds, err := mp.GetCreds()

	assert.NoError(t, err)
	assert.Len(t, creds, 1)
	assert.NotNil(t, creds[0].Password)
	assert.Equal(t, decryptedPassword, *creds[0].Password)

	// Verify context.Background() used
	mocks.apiserver.AssertCalled(t, "GetMetricsCreds", context.Background(), "agent-123")
}

func TestGetCreds_MultipleCredentials(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	password1 := "password-1"
	password2 := "password-2"
	password3 := "password-3"

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").
		Return([]credential.Credential{
			{PasswdEnc: "enc-pass-1"},
			{PasswdEnc: "enc-pass-2"},
			{PasswdEnc: "enc-pass-3"},
		}, nil)
	mocks.crypto.On("LoadPrivateKey", "/test/key/path").
		Return(testPrivateKey, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "enc-pass-1", testPrivateKey).
		Return(password1, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "enc-pass-2", testPrivateKey).
		Return(password2, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "enc-pass-3", testPrivateKey).
		Return(password3, nil)

	creds, err := mp.GetCreds()

	assert.NoError(t, err)
	assert.Len(t, creds, 3)
	assert.Equal(t, password1, *creds[0].Password)
	assert.Equal(t, password2, *creds[1].Password)
	assert.Equal(t, password3, *creds[2].Password)

	mocks.crypto.AssertNumberOfCalls(t, "DecryptWithPrivateKey", 3)
}

func TestGetCreds_DecryptionFailureAtSecondCredential(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	password1 := "password-1"
	decryptErr := errors.New("corruption detected")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").
		Return([]credential.Credential{
			{PasswdEnc: "enc-pass-1"},
			{PasswdEnc: "enc-pass-2"},
			{PasswdEnc: "enc-pass-3"},
		}, nil)
	mocks.crypto.On("LoadPrivateKey", "/test/key/path").
		Return(testPrivateKey, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "enc-pass-1", testPrivateKey).
		Return(password1, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "enc-pass-2", testPrivateKey).
		Return("", decryptErr)

	creds, err := mp.GetCreds()

	assert.Nil(t, creds)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to decrypt password for credential 1")
	assert.ErrorIs(t, err, decryptErr)
}

// Push Tests - Agent State Validation
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
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{ConnectionsTotal: 5}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		hasNetwork := false
		hasDb := false
		hasStorage := false
		for _, ms := range p.MetricSets {
			if ms.Type == domainmetrics.MetricTypeNetwork {
				hasNetwork = true
			}
			if ms.Type == domainmetrics.MetricTypePostgreSQLDatabase {
				hasDb = true
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
func TestPush_DatabaseMetricsFailure_StillPushesSystemMetrics(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}
	collectErr := errors.New("connection refused")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{}, collectErr)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.MatchedBy(func(p domainmetrics.MetricPayload) bool {
		hasSys := false
		hasNetwork := false
		hasStorage := false
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
		}
		return hasSys && hasNetwork && hasStorage
	})).Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.collector.AssertExpectations(t)
	mocks.apiserver.AssertExpectations(t)
}

// Push Tests - All Collections Fail
func TestPush_AllCollectionsFail(t *testing.T) {
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

	err := mp.Push(testCred)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all metrics collection failed")
	mocks.apiserver.AssertNotCalled(t, "PushMetrics")
}

func TestPush_APIServerPushFailure(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}
	pushErr := errors.New("network timeout")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{ConnectionsTotal: 5}, nil)
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
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{
			ConnectionsTotal:      10,
			MaxConnections:        100,
			CacheHitRatio:         95.5,
			TransactionsPerSecond: 100.0,
			CommittedTxPerSecond:  98.0,
			BlocksReadPerSecond:   5.5,
			ReplicationLagSeconds: 0,
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
		if len(p.MetricSets) != 4 {
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
	testCred := credential.Credential{DataDirectory: "/data"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{}, nil)
	mocks.apiserver.On("PushMetrics", mock.MatchedBy(func(ctx context.Context) bool {
		return ctx != nil
	}), mock.Anything).
		Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}

// Edge case tests
func TestGetCreds_ContextIsBackground(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.MatchedBy(func(ctx context.Context) bool {
		// Verify it's background context (no deadline, no cancellation)
		_, hasDeadline := ctx.Deadline()
		return !hasDeadline && ctx.Err() == nil
	}), "agent-123").Return([]credential.Credential{}, nil)

	_, err := mp.GetCreds()
	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}

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
	mocks.collector.On("Collect", mock.MatchedBy(func(c credential.Credential) bool {
		return c.Host == testCred.Host &&
			c.Port == testCred.Port &&
			c.Username == testCred.Username &&
			c.Dialect == testCred.Dialect
	})).Return(domainmetrics.PostgreSQLDatabaseMetrics{}, nil)
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.Anything).
		Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.collector.AssertExpectations(t)
}

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

// Verifies storage metrics are included in the payload
func TestPush_IncludesStorageMetrics(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{DataDirectory: "/data"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	setupStorageCollectorMocks(mocks.storagecollector)
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{}, nil)
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
	testCred := credential.Credential{DataDirectory: "/data"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	mocks.storagecollector.On("Collect", mock.Anything).
		Return(nil, errors.New("storage collection failed"))
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{}, nil)
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
	testCred := credential.Credential{DataDirectory: "/data"}

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
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{}, nil)
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
func TestPush_StorageMetricsWithAttributes(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{DataDirectory: "/data"}

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
	mocks.collector.On("Collect", testCred).
		Return(domainmetrics.PostgreSQLDatabaseMetrics{}, nil)
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
