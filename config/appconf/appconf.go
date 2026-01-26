// Package appconf contains app related configurations
package appconf

import (
	"os"
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
