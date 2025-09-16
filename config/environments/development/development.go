// Package development contains development configuration of the app
package development

import (
	"os"
	"strings"
)

var (
	APIServerHost string
	APIServerPORT string
)

func init() {
	APIServerHost = os.Getenv("SH_API_SERVER_HOST")
	if strings.TrimSpace(APIServerHost) == "" {
		APIServerHost = "0.0.0.0"
	}

	APIServerPORT = os.Getenv("SH_API_SERVER_PORT")
	if strings.TrimSpace(APIServerPORT) == "" {
		APIServerPORT = "8080"
	}
}
