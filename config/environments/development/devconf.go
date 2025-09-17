// Package development contains development configuration of the app
package development

import (
	"fmt"
	"hostlink/config"
	"os"
	"strings"
)

type devconf struct{}

func New() config.AppConfiger {
	return devconf{}
}

func (dc devconf) GetPort() string {
	appPort := os.Getenv("SH_APP_PORT")
	if strings.TrimSpace(appPort) == "" {
		appPort = "8080"
	}
	return appPort
}

func (dc devconf) getHost() string {
	host := os.Getenv("SH_API_SERVER_HOST")
	if strings.TrimSpace(host) == "" {
		host = "localhost"
	}
	return host
}

func (dc devconf) GetDBURL() string {
	dbURL := os.Getenv("SH_DB_URL")
	if strings.TrimSpace(dbURL) == "" {
		dbURL = "file:hostlink.db"
	}
	return dbURL
}

func (dc devconf) GetControlPlaneURL() string {
	cpURL := os.Getenv("SH_CONTROL_PLANE_URL")
	if strings.TrimSpace(cpURL) == "" {
		cpURL = fmt.Sprintf("http://%s:%s", dc.getHost(), dc.GetPort())
	}
	return cpURL
}
