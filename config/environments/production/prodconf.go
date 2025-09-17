// Package production contains development configuration of the app
package production

import (
	"hostlink/config"
	"os"
	"strings"
)

type prodconf struct{}

func New() config.AppConfiger {
	return prodconf{}
}

func (pc prodconf) GetPort() string {
	appPort := os.Getenv("SH_APP_PORT")
	if strings.TrimSpace(appPort) == "" {
		appPort = "8080"
	}
	return appPort
}

func (pc prodconf) GetDBURL() string {
	dbURL := os.Getenv("SH_DB_URL")
	if strings.TrimSpace(dbURL) == "" {
		dbURL = "/var/lib/selfhost/storage/selfhost.db"
	}
	return dbURL
}

func (pc prodconf) GetControlPlaneURL() string {
	cpURL := os.Getenv("SH_CONTROL_PLANE_URL")
	if strings.TrimSpace(cpURL) == "" {
		cpURL = "https://api.selfhost.dev"
	}
	return cpURL
}
