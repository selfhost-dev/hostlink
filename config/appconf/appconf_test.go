package appconf

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSelfUpdateEnabled_DefaultTrue(t *testing.T) {
	t.Setenv("HOSTLINK_SELF_UPDATE_ENABLED", "")
	assert.True(t, SelfUpdateEnabled())
}

func TestSelfUpdateEnabled_ExplicitTrue(t *testing.T) {
	t.Setenv("HOSTLINK_SELF_UPDATE_ENABLED", "true")
	assert.True(t, SelfUpdateEnabled())
}

func TestSelfUpdateEnabled_ExplicitFalse(t *testing.T) {
	t.Setenv("HOSTLINK_SELF_UPDATE_ENABLED", "false")
	assert.False(t, SelfUpdateEnabled())
}

func TestSelfUpdateEnabled_InvalidFallsToDefault(t *testing.T) {
	t.Setenv("HOSTLINK_SELF_UPDATE_ENABLED", "garbage")
	assert.True(t, SelfUpdateEnabled())
}

func TestUpdateCheckInterval_Default1h(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_CHECK_INTERVAL", "")
	assert.Equal(t, 1*time.Hour, UpdateCheckInterval())
}

func TestUpdateCheckInterval_CustomValue(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_CHECK_INTERVAL", "30m")
	assert.Equal(t, 30*time.Minute, UpdateCheckInterval())
}

func TestUpdateCheckInterval_ClampedToMin(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_CHECK_INTERVAL", "10s")
	assert.Equal(t, 1*time.Minute, UpdateCheckInterval())
}

func TestUpdateCheckInterval_ClampedToMax(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_CHECK_INTERVAL", "48h")
	assert.Equal(t, 24*time.Hour, UpdateCheckInterval())
}

func TestUpdateCheckInterval_InvalidFallsToDefault(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_CHECK_INTERVAL", "garbage")
	assert.Equal(t, 1*time.Hour, UpdateCheckInterval())
}

func TestUpdateLockTimeout_Default5m(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_LOCK_TIMEOUT", "")
	assert.Equal(t, 5*time.Minute, UpdateLockTimeout())
}

func TestUpdateLockTimeout_CustomValue(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_LOCK_TIMEOUT", "10m")
	assert.Equal(t, 10*time.Minute, UpdateLockTimeout())
}

func TestUpdateLockTimeout_ClampedToMin(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_LOCK_TIMEOUT", "10s")
	assert.Equal(t, 1*time.Minute, UpdateLockTimeout())
}

func TestUpdateLockTimeout_ClampedToMax(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_LOCK_TIMEOUT", "2h")
	assert.Equal(t, 30*time.Minute, UpdateLockTimeout())
}

func TestUpdateLockTimeout_InvalidFallsToDefault(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_LOCK_TIMEOUT", "garbage")
	assert.Equal(t, 5*time.Minute, UpdateLockTimeout())
}
