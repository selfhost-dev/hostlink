package metrics

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/pgmetrics"
	"strings"
	"testing"

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

func (m *MockAPIServer) PushPostgreSQLMetrics(ctx context.Context, metrics domainmetrics.PostgreSQLMetrics, agentID string) error {
	args := m.Called(ctx, metrics, agentID)
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

func (m *MockCollector) Collect(cred credential.Credential) (pgmetrics.DatabaseMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(pgmetrics.DatabaseMetrics), args.Error(1)
}

type MockCommandExecutor struct {
	mock.Mock
}

func (m *MockCommandExecutor) Execute(ctx context.Context, command string) (string, error) {
	args := m.Called(ctx, command)
	return args.String(0), args.Error(1)
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
	apiserver   *MockAPIServer
	agentstate  *MockAgentState
	collector   *MockCollector
	cmdExecutor *MockCommandExecutor
	crypto      *MockCrypto
}

func setupTestMetricsPusher() (*metricspusher, *testMocks) {
	mocks := &testMocks{
		apiserver:   new(MockAPIServer),
		agentstate:  new(MockAgentState),
		collector:   new(MockCollector),
		cmdExecutor: new(MockCommandExecutor),
		crypto:      new(MockCrypto),
	}

	mp := NewWithDependencies(
		mocks.apiserver,
		mocks.agentstate,
		mocks.collector,
		mocks.cmdExecutor,
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
	assert.Equal(t, mocks.cmdExecutor, mp.cmdExecutor)
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
	mocks.apiserver.AssertNotCalled(t, "PushPostgreSQLMetrics")
}

// Push Tests - System Metrics Collection Failure
func TestPush_SystemMetricsFailure(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.cmdExecutor.On("Execute", mock.Anything, mock.Anything).
		Return("", errors.New("command failed"))

	err := mp.Push(testCred)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "system metrics")
	mocks.collector.AssertNotCalled(t, "Collect")
	mocks.apiserver.AssertNotCalled(t, "PushPostgreSQLMetrics")
}

func TestPush_DatabaseMetricsFailure(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}
	collectErr := errors.New("connection refused")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupCmdExecutorMocks(mocks.cmdExecutor)
	mocks.collector.On("Collect", testCred).
		Return(pgmetrics.DatabaseMetrics{}, collectErr)

	err := mp.Push(testCred)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "database metrics")
	mocks.collector.AssertExpectations(t)
	mocks.apiserver.AssertNotCalled(t, "PushPostgreSQLMetrics")
}

func TestPush_APIServerPushFailure(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost", DataDirectory: "/var/lib/postgresql"}
	pushErr := errors.New("network timeout")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupCmdExecutorMocks(mocks.cmdExecutor)
	mocks.collector.On("Collect", testCred).
		Return(pgmetrics.DatabaseMetrics{ConnectionsTotal: 5}, nil)
	mocks.apiserver.On("PushPostgreSQLMetrics", mock.Anything, mock.Anything, "agent-123").
		Return(pushErr)

	err := mp.Push(testCred)

	assert.Equal(t, pushErr, err)
	mocks.apiserver.AssertExpectations(t)
}

func TestPush_Success(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{
		Host:          "localhost",
		Port:          5432,
		Username:      "postgres",
		DataDirectory: "/var/lib/postgresql",
	}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupCmdExecutorMocks(mocks.cmdExecutor)
	mocks.collector.On("Collect", testCred).
		Return(pgmetrics.DatabaseMetrics{
			ConnectionsTotal:      10,
			CacheHitRatio:         95.5,
			TransactionsPerSecond: 100.0,
			ReplicationLagSeconds: 0,
		}, nil)
	mocks.apiserver.On("PushPostgreSQLMetrics", mock.Anything, mock.MatchedBy(func(m domainmetrics.PostgreSQLMetrics) bool {
		return m.CPUPercent == 25.0 &&
			m.MemoryPercent == 50.0 &&
			m.ConnectionsTotal == 10 &&
			m.CacheHitRatio == 95.5
	}), "agent-123").Return(nil)

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
	setupCmdExecutorMocks(mocks.cmdExecutor)
	mocks.collector.On("Collect", testCred).
		Return(pgmetrics.DatabaseMetrics{}, nil)
	mocks.apiserver.On("PushPostgreSQLMetrics", mock.MatchedBy(func(ctx context.Context) bool {
		return ctx != nil
	}), mock.Anything, "agent-123").
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
	setupCmdExecutorMocks(mocks.cmdExecutor)
	mocks.collector.On("Collect", mock.MatchedBy(func(c credential.Credential) bool {
		return c.Host == testCred.Host &&
			c.Port == testCred.Port &&
			c.Username == testCred.Username &&
			c.Dialect == testCred.Dialect
	})).Return(pgmetrics.DatabaseMetrics{}, nil)
	mocks.apiserver.On("PushPostgreSQLMetrics", mock.Anything, mock.Anything, "agent-456").
		Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.collector.AssertExpectations(t)
}

func setupCmdExecutorMocks(executor *MockCommandExecutor) {
	executor.On("Execute", mock.Anything, mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "top")
	})).Return("25.0", nil)
	executor.On("Execute", mock.Anything, mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "free") && !strings.Contains(cmd, "Swap")
	})).Return("50.0", nil)
	executor.On("Execute", mock.Anything, mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "cat")
	})).Return("1.5 2.0 2.5", nil)
	executor.On("Execute", mock.Anything, mock.MatchedBy(func(cmd string) bool {
		return strings.Contains(cmd, "Swap")
	})).Return("10.0", nil)
	executor.On("Execute", mock.Anything, mock.MatchedBy(func(cmd string) bool {
		return strings.HasPrefix(cmd, "df")
	})).Return("75", nil)
}
