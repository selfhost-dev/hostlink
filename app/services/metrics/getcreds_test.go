package metrics

import (
	"context"
	"errors"
	"testing"

	"hostlink/domain/credential"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

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

	mocks.apiserver.AssertCalled(t, "GetMetricsCreds", mock.Anything, "agent-123")
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
func TestGetCreds_ContextIsBounded(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()

	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.MatchedBy(func(ctx context.Context) bool {
		// Bounded, so a stalled fetch cannot hold the metrics beat (selfhost#3963).
		_, hasDeadline := ctx.Deadline()
		return hasDeadline && ctx.Err() == nil
	}), "agent-123").Return([]credential.Credential{}, nil)

	_, err := mp.GetCreds()
	assert.NoError(t, err)
	mocks.apiserver.AssertExpectations(t)
}
