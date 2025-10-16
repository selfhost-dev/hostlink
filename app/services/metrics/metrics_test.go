package metrics

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"hostlink/domain/credential"
	"hostlink/domain/metrics"
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

func (m *MockAPIServer) PushPostgreSQLMetrics(ctx context.Context, metrics metrics.PostgreSQLMetrics, agentID string) error {
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

func (m *MockCollector) Collect(cred credential.Credential) (metrics.PostgreSQLMetrics, error) {
	args := m.Called(cred)
	return args.Get(0).(metrics.PostgreSQLMetrics), args.Error(1)
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
	apiserver  *MockAPIServer
	agentstate *MockAgentState
	collector  *MockCollector
	crypto     *MockCrypto
}

func setupTestMetricsPusher() (*metricspusher, *testMocks) {
	mocks := &testMocks{
		apiserver:  new(MockAPIServer),
		agentstate: new(MockAgentState),
		collector:  new(MockCollector),
		crypto:     new(MockCrypto),
	}

	mp := NewWithDependencies(
		mocks.apiserver,
		mocks.agentstate,
		mocks.collector,
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
	testCred := credential.Credential{}

	mocks.agentstate.On("GetAgentID").Return("")

	err := mp.Push(testCred)

	assert.EqualError(t, err, "agent not registered: missing agent ID")
	mocks.agentstate.AssertExpectations(t)
	mocks.collector.AssertNotCalled(t, "Collect")
	mocks.apiserver.AssertNotCalled(t, "PushPostgreSQLMetrics")
}

// Push Tests - Metrics Collection
func TestPush_CollectionFailure(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost"}
	collectErr := errors.New("connection refused")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.collector.On("Collect", testCred).
		Return(metrics.PostgreSQLMetrics{}, collectErr)

	err := mp.Push(testCred)

	assert.Equal(t, collectErr, err)
	mocks.collector.AssertExpectations(t)
	mocks.apiserver.AssertNotCalled(t, "PushPostgreSQLMetrics")
}

func TestPush_APIServerPushFailure(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{Host: "localhost"}
	testMetrics := metrics.PostgreSQLMetrics{CPUPercent: 45.5}
	pushErr := errors.New("network timeout")

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.collector.On("Collect", testCred).
		Return(testMetrics, nil)
	mocks.apiserver.On("PushPostgreSQLMetrics", mock.Anything, testMetrics, "agent-123").
		Return(pushErr)

	err := mp.Push(testCred)

	assert.Equal(t, pushErr, err)
	mocks.apiserver.AssertExpectations(t)
}

func TestPush_Success(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{
		Host:     "localhost",
		Port:     5432,
		Username: "postgres",
	}
	testMetrics := metrics.PostgreSQLMetrics{
		CPUPercent:    45.5,
		MemoryPercent: 78.2,
	}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.collector.On("Collect", testCred).
		Return(testMetrics, nil)
	mocks.apiserver.On("PushPostgreSQLMetrics", mock.Anything, testMetrics, "agent-123").
		Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.agentstate.AssertExpectations(t)
	mocks.collector.AssertExpectations(t)
	mocks.apiserver.AssertExpectations(t)

	// Verify context.Background() used
	mocks.apiserver.AssertCalled(t, "PushPostgreSQLMetrics", context.Background(), testMetrics, "agent-123")
}

func TestPush_ContextPropagation(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	testCred := credential.Credential{}
	testMetrics := metrics.PostgreSQLMetrics{}

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.collector.On("Collect", testCred).
		Return(testMetrics, nil)
	mocks.apiserver.On("PushPostgreSQLMetrics", mock.MatchedBy(func(ctx context.Context) bool {
		return ctx != nil
	}), testMetrics, "agent-123").
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
		Host:     "db.example.com",
		Port:     5432,
		Username: "admin",
		Dialect:  "postgresql",
	}
	testMetrics := metrics.PostgreSQLMetrics{}

	mocks.agentstate.On("GetAgentID").Return("agent-456")
	mocks.collector.On("Collect", mock.MatchedBy(func(c credential.Credential) bool {
		return c.Host == testCred.Host &&
			c.Port == testCred.Port &&
			c.Username == testCred.Username &&
			c.Dialect == testCred.Dialect
	})).Return(testMetrics, nil)
	mocks.apiserver.On("PushPostgreSQLMetrics", mock.Anything, testMetrics, "agent-456").
		Return(nil)

	err := mp.Push(testCred)

	assert.NoError(t, err)
	mocks.collector.AssertExpectations(t)
}
