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
