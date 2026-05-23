// Package appconf contains app related configurations
package appconf

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"

	"hostlink/config"
	devconf "hostlink/config/environments/development"
	prodconf "hostlink/config/environments/production"
)

var appconf config.AppConfiger

func Port() string {
	return appconf.GetPort()
}

func DBURL() string {
	return appconf.GetDBURL()
}

func ControlPlaneURL() string {
	return appconf.GetControlPlaneURL()
}

func AgentPrivateKeyPath() string {
	if path := os.Getenv("HOSTLINK_PRIVATE_KEY_PATH"); path != "" {
		return path
	}
	return "/var/lib/hostlink/agent.key"
}

func AgentFingerprintPath() string {
	if path := os.Getenv("HOSTLINK_FINGERPRINT_PATH"); path != "" {
		return path
	}
	return "/var/lib/hostlink/fingerprint.json"
}

func AgentTokenID() string {
	return os.Getenv("HOSTLINK_TOKEN_ID")
}

func AgentTokenKey() string {
	return os.Getenv("HOSTLINK_TOKEN_KEY")
}

func AgentStatePath() string {
	if path := os.Getenv("HOSTLINK_STATE_PATH"); path != "" {
		return path
	}
	return "/var/lib/hostlink"
}

func LocalTaskStorePath() string {
	if path := strings.TrimSpace(os.Getenv("HOSTLINK_LOCAL_STORE_PATH")); path != "" {
		return path
	}
	return filepath.Join(AgentStatePath(), "task_store.db")
}

func LocalTaskStoreSpoolCapBytes() int64 {
	return parseInt64Positive("HOSTLINK_LOCAL_STORE_SPOOL_CAP_BYTES", 64*1024*1024)
}

func LocalTaskStoreTerminalReserveBytes() int64 {
	return parseInt64Positive("HOSTLINK_LOCAL_STORE_TERMINAL_RESERVE_BYTES", 1024*1024)
}

// InstallPath returns the target install path for the hostlink binary.
// Controlled by HOSTLINK_INSTALL_PATH (default: /usr/bin/hostlink).
func InstallPath() string {
	if path := os.Getenv("HOSTLINK_INSTALL_PATH"); path != "" {
		return path
	}
	return "/usr/bin/hostlink"
}

// SelfUpdateEnabled returns whether the self-update feature is enabled.
// Controlled by HOSTLINK_SELF_UPDATE_ENABLED (default: true).
func SelfUpdateEnabled() bool {
	v := strings.TrimSpace(os.Getenv("HOSTLINK_SELF_UPDATE_ENABLED"))
	if v == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "false", "0", "no":
		return false
	default:
		return true
	}
}

// WebSocketEnabled returns whether the agent WebSocket client is enabled.
// Controlled by HOSTLINK_WS_ENABLED (default: false).
func WebSocketEnabled() bool {
	return parseBoolEnabled("HOSTLINK_WS_ENABLED", false)
}

// WebSocketResultsEnabled returns whether task output and final messages use WebSocket.
// Controlled by HOSTLINK_WS_RESULTS_ENABLED (default: false).
func WebSocketResultsEnabled() bool {
	return parseBoolEnabled("HOSTLINK_WS_RESULTS_ENABLED", false)
}

// WebSocketDeliveryEnabled returns whether task delivery uses WebSocket.
// Controlled by HOSTLINK_WS_DELIVERY_ENABLED (default: false).
func WebSocketDeliveryEnabled() bool {
	return parseBoolEnabled("HOSTLINK_WS_DELIVERY_ENABLED", false)
}

// WebSocketPollingFallbackThreshold returns how long delivery disconnects may pause polling.
// Controlled by HOSTLINK_WS_POLLING_FALLBACK_THRESHOLD (default: 30s, clamped to [0s, 5m]).
func WebSocketPollingFallbackThreshold() time.Duration {
	return parseDurationClamped("HOSTLINK_WS_POLLING_FALLBACK_THRESHOLD", 30*time.Second, 0, 5*time.Minute)
}

// WebSocketURL returns the agent WebSocket gateway URL.
// Controlled by HOSTLINK_WS_URL, otherwise derived from SH_CONTROL_PLANE_URL.
func WebSocketURL() string {
	if rawURL := strings.TrimSpace(os.Getenv("HOSTLINK_WS_URL")); rawURL != "" {
		return rawURL
	}

	parsed, err := url.Parse(ControlPlaneURL())
	if err != nil {
		log.Warnf("invalid control plane URL %q, using as websocket URL base", ControlPlaneURL())
		return ControlPlaneURL() + "/api/v1/agents/ws"
	}

	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	}
	parsed.Path = path.Join(parsed.Path, "/api/v1/agents/ws")
	return parsed.String()
}

// WebSocketReconnectMin returns the initial WebSocket reconnect delay.
// Controlled by HOSTLINK_WS_RECONNECT_MIN (default: 1s, clamped to [100ms, 5m]).
func WebSocketReconnectMin() time.Duration {
	return parseDurationClamped("HOSTLINK_WS_RECONNECT_MIN", time.Second, 100*time.Millisecond, 5*time.Minute)
}

// WebSocketReconnectMax returns the maximum WebSocket reconnect delay.
// Controlled by HOSTLINK_WS_RECONNECT_MAX (default: 5m, clamped to [1s, 1h]).
func WebSocketReconnectMax() time.Duration {
	return parseDurationClamped("HOSTLINK_WS_RECONNECT_MAX", 5*time.Minute, time.Second, time.Hour)
}

// WebSocketPingInterval returns the WebSocket keepalive ping interval.
// Controlled by HOSTLINK_WS_PING_INTERVAL (default: 30s, clamped to [5s, 5m]).
func WebSocketPingInterval() time.Duration {
	return parseDurationClamped("HOSTLINK_WS_PING_INTERVAL", 30*time.Second, 5*time.Second, 5*time.Minute)
}

// RegistrationRetryInitialDelay returns the first retry delay for agent registration.
// Controlled by HOSTLINK_REGISTRATION_RETRY_INITIAL_DELAY (default: 10s, clamped to [10ms, 5m]).
func RegistrationRetryInitialDelay() time.Duration {
	return parseDurationClamped("HOSTLINK_REGISTRATION_RETRY_INITIAL_DELAY", 10*time.Second, 10*time.Millisecond, 5*time.Minute)
}

// TaskPollInterval returns the interval between task polling attempts.
// Controlled by HOSTLINK_TASK_POLL_INTERVAL (default: 10s, clamped to [10ms, 5m]).
func TaskPollInterval() time.Duration {
	return parseDurationClamped("HOSTLINK_TASK_POLL_INTERVAL", 10*time.Second, 10*time.Millisecond, 5*time.Minute)
}

// TaskOutputFlushInterval returns how often buffered task output is flushed.
// Controlled by HOSTLINK_TASK_OUTPUT_FLUSH_INTERVAL (default: 100ms, clamped to [1ms, 5s]).
func TaskOutputFlushInterval() time.Duration {
	return parseDurationClamped("HOSTLINK_TASK_OUTPUT_FLUSH_INTERVAL", 100*time.Millisecond, time.Millisecond, 5*time.Second)
}

// TaskOutputFlushThreshold returns the buffered task output byte threshold.
// Controlled by HOSTLINK_TASK_OUTPUT_FLUSH_THRESHOLD (default: 16KiB).
func TaskOutputFlushThreshold() int {
	return int(parseInt64Positive("HOSTLINK_TASK_OUTPUT_FLUSH_THRESHOLD", 16*1024))
}

// MetricsPushInterval returns the interval between metrics push attempts.
// Controlled by HOSTLINK_METRICS_PUSH_INTERVAL (default: 20s, clamped to [10ms, 5m]).
func MetricsPushInterval() time.Duration {
	return parseDurationClamped("HOSTLINK_METRICS_PUSH_INTERVAL", 20*time.Second, 10*time.Millisecond, 5*time.Minute)
}

// HeartbeatInterval returns the interval between heartbeat attempts.
// Controlled by HOSTLINK_HEARTBEAT_INTERVAL (default: 5s, clamped to [10ms, 5m]).
func HeartbeatInterval() time.Duration {
	return parseDurationClamped("HOSTLINK_HEARTBEAT_INTERVAL", 5*time.Second, 10*time.Millisecond, 5*time.Minute)
}

// UpdateCheckInterval returns the interval between update checks.
// Controlled by HOSTLINK_UPDATE_CHECK_INTERVAL (default: 5m, clamped to [1m, 24h]).
func UpdateCheckInterval() time.Duration {
	const (
		// TODO(SLFHOST-11): revert to 1*time.Hour once self-update debugging is complete
		defaultInterval = 5 * time.Minute
		minInterval     = 1 * time.Minute
		maxInterval     = 24 * time.Hour
	)
	return parseDurationClamped("HOSTLINK_UPDATE_CHECK_INTERVAL", defaultInterval, minInterval, maxInterval)
}

// UpdateLockTimeout returns the lock expiration duration for self-updates.
// Controlled by HOSTLINK_UPDATE_LOCK_TIMEOUT (default: 5m, clamped to [1m, 30m]).
func UpdateLockTimeout() time.Duration {
	const (
		defaultTimeout = 5 * time.Minute
		minTimeout     = 1 * time.Minute
		maxTimeout     = 30 * time.Minute
	)
	return parseDurationClamped("HOSTLINK_UPDATE_LOCK_TIMEOUT", defaultTimeout, minTimeout, maxTimeout)
}

// parseDurationClamped reads a duration from an environment variable, clamping
// it to [min, max]. Returns defaultVal if the env var is empty or unparseable.
func parseDurationClamped(envVar string, defaultVal, min, max time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(envVar))
	if v == "" {
		return defaultVal
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		log.Warnf("invalid %s value %q, using default %s", envVar, v, defaultVal)
		return defaultVal
	}
	if d < min {
		log.Warnf("%s value %s below minimum %s, clamping to %s", envVar, d, min, min)
		return min
	}
	if d > max {
		log.Warnf("%s value %s above maximum %s, clamping to %s", envVar, d, max, max)
		return max
	}
	return d
}

func parseInt64Positive(envVar string, defaultVal int64) int64 {
	v := strings.TrimSpace(os.Getenv(envVar))
	if v == "" {
		return defaultVal
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		log.Warnf("invalid %s value %q, using default %d", envVar, v, defaultVal)
		return defaultVal
	}
	return n
}

func parseBoolEnabled(envVar string, defaultVal bool) bool {
	v := strings.TrimSpace(os.Getenv(envVar))
	if v == "" {
		return defaultVal
	}
	switch strings.ToLower(v) {
	case "true", "1", "yes":
		return true
	default:
		return false
	}
}

func init() {
	env := os.Getenv("APP_ENV")

	switch env {
	case "production":
		appconf = prodconf.New()
	case "development":
		appconf = devconf.New()
	default:
		appconf = devconf.New()
	}
}
