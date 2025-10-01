// Package appconf contains app related configurations
package appconf

import (
	"hostlink/config"
	devconf "hostlink/config/environments/development"
	prodconf "hostlink/config/environments/production"
	"os"
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
