package clickhousemetrics

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real 26.8 wording for a node with no <zookeeper>/<keeper> section:
// Context::getZooKeeper -> zkutil::getZooKeeperConfigName throws
// NO_ELEMENTS_IN_CONFIG (139), not NO_ZOOKEEPER (225).
const noKeeperConfigBody = "Code: 139. DB::Exception: There is no Zookeeper configuration in server config. (NO_ELEMENTS_IN_CONFIG) (version 26.8.9.10 (official build))\n"

func TestQuery_ErrorCarriesClickHouseCode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		header   string
		body     string
		wantCode int
	}{
		{"header wins", http.StatusInternalServerError, "999", "Code: 139. DB::Exception: x", 999},
		{"header only", http.StatusInternalServerError, "225", "boom", 225},
		{"body only", http.StatusInternalServerError, "", noKeeperConfigBody, 139},
		{"body after a prefix", http.StatusForbidden, "", "Received exception from server:\nCode: 497. DB::Exception: default: Not enough privileges. (ACCESS_DENIED)", 497},
		{"no code anywhere", http.StatusBadGateway, "", "<html>502 Bad Gateway</html>", 0},
		{"garbage header falls back to body", http.StatusInternalServerError, "abc", "Code: 47. DB::Exception: Unknown identifier", 47},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("X-ClickHouse-Exception-Code", tc.header)
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			c := New().(*collector)
			_, err := c.query(srv.URL+"/", "u", "p", "SELECT 1")
			require.Error(t, err)

			var qe *queryError
			require.True(t, errors.As(err, &qe), "non-200 responses must surface as *queryError")
			assert.Equal(t, tc.status, qe.HTTPStatus)
			assert.Equal(t, tc.wantCode, qe.Code)
			// The message format callers log today is unchanged.
			assert.Contains(t, err.Error(), fmt.Sprintf("clickhouse http %d: ", tc.status))
		})
	}
}

func TestParseExceptionCode(t *testing.T) {
	assert.Equal(t, 139, parseExceptionCode("", noKeeperConfigBody))
	assert.Equal(t, 225, parseExceptionCode(" 225 ", ""))
	assert.Equal(t, 999, parseExceptionCode("", "Code: 999. Coordination::Exception: Connection loss. (KEEPER_EXCEPTION)"))
	assert.Equal(t, 0, parseExceptionCode("", "no code here"))
	assert.Equal(t, 0, parseExceptionCode("", "Error Code: abc"))
}

func TestQuery_TransportErrorIsNotAQueryError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL + "/"
	srv.Close()

	c := New().(*collector)
	_, err := c.query(url, "u", "p", "SELECT 1")
	require.Error(t, err)
	var qe *queryError
	assert.False(t, errors.As(err, &qe))
}
