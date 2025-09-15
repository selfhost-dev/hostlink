// Package appconf contains app related configurations
package appconf

import (
	"os"
	"strings"
)

var (
	DBPath string
	AppEnv string
)

func init() {
	DBPath = os.Getenv("SH_DB_PATH")
	if strings.TrimSpace(DBPath) == "" {
		DBPath = "file:hostlink.db"
	}

	AppEnv = os.Getenv("APP_ENV")
	if strings.TrimSpace(AppEnv) == "" {
		AppEnv = "development"
	}
}
