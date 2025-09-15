// Package production contains development configuration of the app
package production

import (
	"os"
	"strings"
)

var (
	APIServerHost string
	APIServerPORT string
)

func init() {
	APIServerHost = os.Getenv("SH_API_SERVER_URL")
	if strings.TrimSpace(APIServerHost) == "" {
		APIServerHost = "https://api.selfhost.dev"
	}
}
