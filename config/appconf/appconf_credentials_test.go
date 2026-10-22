package appconf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAPICredentialsTimeout_Default10s(t *testing.T) {
	t.Setenv("HOSTLINK_API_CREDENTIALS_TIMEOUT", "")
	assert.Equal(t, 10*time.Second, APICredentialsTimeout())
}

func TestAPICredentialsTimeout_CustomValue(t *testing.T) {
	t.Setenv("HOSTLINK_API_CREDENTIALS_TIMEOUT", "3s")
	assert.Equal(t, 3*time.Second, APICredentialsTimeout())
}

func TestAPICredentialsTimeout_ClampedToMin(t *testing.T) {
	t.Setenv("HOSTLINK_API_CREDENTIALS_TIMEOUT", "500ms")
	assert.Equal(t, time.Second, APICredentialsTimeout())
}

func TestAPICredentialsTimeout_ClampedToMax(t *testing.T) {
	t.Setenv("HOSTLINK_API_CREDENTIALS_TIMEOUT", "1m")
	assert.Equal(t, 25*time.Second, APICredentialsTimeout())
}

func TestAPICredentialsTimeout_InvalidFallsToDefault(t *testing.T) {
	t.Setenv("HOSTLINK_API_CREDENTIALS_TIMEOUT", "garbage")
	assert.Equal(t, 10*time.Second, APICredentialsTimeout())
}
