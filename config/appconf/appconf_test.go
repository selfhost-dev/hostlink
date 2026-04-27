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

func TestUpdateCheckInterval_Default5m(t *testing.T) {
	t.Setenv("HOSTLINK_UPDATE_CHECK_INTERVAL", "")
	assert.Equal(t, 5*time.Minute, UpdateCheckInterval())
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
	assert.Equal(t, 5*time.Minute, UpdateCheckInterval())
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

func TestInstallPath_Default(t *testing.T) {
	t.Setenv("HOSTLINK_INSTALL_PATH", "")
	assert.Equal(t, "/usr/bin/hostlink", InstallPath())
}

func TestInstallPath_CustomValue(t *testing.T) {
	t.Setenv("HOSTLINK_INSTALL_PATH", "/opt/hostlink/bin/hostlink")
	assert.Equal(t, "/opt/hostlink/bin/hostlink", InstallPath())
}

func TestWebSocketEnabled_DefaultFalse(t *testing.T) {
	t.Setenv("HOSTLINK_WS_ENABLED", "")
	assert.False(t, WebSocketEnabled())
}

func TestWebSocketEnabled_ExplicitTrue(t *testing.T) {
	t.Setenv("HOSTLINK_WS_ENABLED", "true")
	assert.True(t, WebSocketEnabled())
}

func TestWebSocketEnabled_ExplicitFalse(t *testing.T) {
	t.Setenv("HOSTLINK_WS_ENABLED", "0")
	assert.False(t, WebSocketEnabled())
}

func TestWebSocketURL_DerivesWSSFromHTTPSControlPlane(t *testing.T) {
	t.Setenv("HOSTLINK_WS_URL", "")
	t.Setenv("SH_CONTROL_PLANE_URL", "https://api.selfhost.dev")

	assert.Equal(t, "wss://api.selfhost.dev/api/v1/agents/ws", WebSocketURL())
}

func TestWebSocketURL_DerivesWSFromHTTPControlPlane(t *testing.T) {
	t.Setenv("HOSTLINK_WS_URL", "")
	t.Setenv("SH_CONTROL_PLANE_URL", "http://localhost:3000")

	assert.Equal(t, "ws://localhost:3000/api/v1/agents/ws", WebSocketURL())
}

func TestWebSocketURL_OverrideWins(t *testing.T) {
	t.Setenv("HOSTLINK_WS_URL", "ws://127.0.0.1:9090/custom")
	t.Setenv("SH_CONTROL_PLANE_URL", "https://api.selfhost.dev")

	assert.Equal(t, "ws://127.0.0.1:9090/custom", WebSocketURL())
}

func TestWebSocketReconnectMin_Default1s(t *testing.T) {
	t.Setenv("HOSTLINK_WS_RECONNECT_MIN", "")
	assert.Equal(t, time.Second, WebSocketReconnectMin())
}

func TestWebSocketReconnectMin_CustomValue(t *testing.T) {
	t.Setenv("HOSTLINK_WS_RECONNECT_MIN", "5s")
	assert.Equal(t, 5*time.Second, WebSocketReconnectMin())
}

func TestWebSocketReconnectMax_Default5m(t *testing.T) {
	t.Setenv("HOSTLINK_WS_RECONNECT_MAX", "")
	assert.Equal(t, 5*time.Minute, WebSocketReconnectMax())
}

func TestWebSocketPingInterval_Default30s(t *testing.T) {
	t.Setenv("HOSTLINK_WS_PING_INTERVAL", "")
	assert.Equal(t, 30*time.Second, WebSocketPingInterval())
}

func TestWebSocketPingInterval_CustomValue(t *testing.T) {
	t.Setenv("HOSTLINK_WS_PING_INTERVAL", "45s")
	assert.Equal(t, 45*time.Second, WebSocketPingInterval())
}
