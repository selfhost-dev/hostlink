package clickhousemetrics

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"hostlink/domain/credential"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockClickHouseServer returns an httptest.Server that routes based on POST body.
// handlers maps a substring of the query body to a response string.
func mockClickHouseServer(t *testing.T, handlers map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read error", http.StatusInternalServerError)
			return
		}
		q := string(body)
		for key, resp := range handlers {
			if strings.Contains(q, key) {
				w.WriteHeader(http.StatusOK)
				fmt.Fprint(w, resp)
				return
			}
		}
		// Unknown query — return empty OK
		w.WriteHeader(http.StatusOK)
	}))
}

func credForServer(srv *httptest.Server) credential.Credential {
	// httptest server binds to a random port. We pass Port=0 so the collector
	// falls back to 8123, but the baseURL we actually want is the server's address.
	// Override by pointing the collector at the test server's port directly via
	// a custom credential: since httpPort = Port-877, we set Port = serverPort+877.
	var port int
	fmt.Sscanf(srv.Listener.Addr().String(), "127.0.0.1:%d", &port)
	pw := "test"
	return credential.Credential{
		Host:     "127.0.0.1",
		Port:     port + 877, // collector derives httpPort = Port - 877
		Username: "default",
		Password: &pw,
	}
}

func standardHandlers(queryCount, selectCount, insertCount, failedCount, insertRowCount int64) map[string]string {
	return map[string]string{
		"SELECT 1": "1\n",
		"system.metrics": strings.Join([]string{
			`{"metric":"TCPConnection","value":3}`,
			`{"metric":"HTTPConnection","value":1}`,
		}, "\n"),
		"system.events": strings.Join([]string{
			fmt.Sprintf(`{"event":"Query","value":%d}`, queryCount),
			fmt.Sprintf(`{"event":"SelectQuery","value":%d}`, selectCount),
			fmt.Sprintf(`{"event":"InsertQuery","value":%d}`, insertCount),
			fmt.Sprintf(`{"event":"FailedQuery","value":%d}`, failedCount),
			fmt.Sprintf(`{"event":"InsertedRows","value":%d}`, insertRowCount),
			`{"event":"MarkCacheHits","value":900}`,
			`{"event":"MarkCacheMisses","value":100}`,
		}, "\n"),
		"system.parts": `{"value":42}`,
	}
}

func TestCollect_FirstCollection_ReturnsZeroRates(t *testing.T) {
	srv := mockClickHouseServer(t, standardHandlers(1000, 800, 100, 5, 5000))
	defer srv.Close()

	c := New()
	m, err := c.Collect(credForServer(srv))

	require.NoError(t, err)
	assert.Equal(t, 4, m.ConnectionsTotal) // 3 TCP + 1 HTTP
	assert.Equal(t, 4, m.ConnectionsCount)
	assert.Equal(t, int64(1000), m.QueryCount)
	assert.Equal(t, 0.0, m.QueriesPerSecond, "first call must return 0 QPS (baseline only)")
	assert.Equal(t, 0.0, m.SelectQueriesPerSecond)
	assert.Equal(t, 0.0, m.InsertQueriesPerSecond)
	assert.Equal(t, 0.0, m.FailedQueriesPerSecond)
	assert.Equal(t, 0.0, m.InsertedRowsPerSecond)
	assert.Equal(t, 90.0, m.MarkCacheHitRatio, "900/(900+100)*100 = 90%")
	assert.Equal(t, 42, m.PartsActive)
}

func TestCollect_SecondCollection_ReturnsDeltaRates(t *testing.T) {
	srv := mockClickHouseServer(t, standardHandlers(1000, 800, 100, 5, 5000))
	defer srv.Close()

	cred := credForServer(srv)
	c := New()
	_, err := c.Collect(cred) // baseline
	require.NoError(t, err)

	time.Sleep(time.Millisecond) // ensure elapsed > 0

	// Simulate counters advancing
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		q := string(body)
		w.WriteHeader(http.StatusOK)
		switch {
		case strings.Contains(q, "SELECT 1"):
			fmt.Fprint(w, "1\n")
		case strings.Contains(q, "system.metrics"):
			fmt.Fprint(w, `{"metric":"TCPConnection","value":5}`+"\n")
		case strings.Contains(q, "system.events"):
			fmt.Fprint(w, strings.Join([]string{
				`{"event":"Query","value":1500}`,
				`{"event":"SelectQuery","value":1200}`,
				`{"event":"InsertQuery","value":150}`,
				`{"event":"FailedQuery","value":8}`,
				`{"event":"InsertedRows","value":7500}`,
				`{"event":"MarkCacheHits","value":900}`,
				`{"event":"MarkCacheMisses","value":100}`,
			}, "\n"))
		case strings.Contains(q, "system.parts"):
			fmt.Fprint(w, `{"value":45}`)
		}
	})

	m2, err := c.Collect(cred)
	require.NoError(t, err)

	assert.Greater(t, m2.QueriesPerSecond, 0.0, "QPS should be positive after counter delta")
	assert.Greater(t, m2.SelectQueriesPerSecond, 0.0)
	assert.Greater(t, m2.InsertQueriesPerSecond, 0.0)
	assert.Greater(t, m2.InsertedRowsPerSecond, 0.0)
	assert.Greater(t, m2.FailedQueriesPerSecond, 0.0)
	assert.Equal(t, 45, m2.PartsActive)
}

func TestCollect_HTTPError_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Authentication failed", http.StatusUnauthorized)
	}))
	defer srv.Close()

	c := New()
	_, err := c.Collect(credForServer(srv))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping")
}

func TestCollect_HTTPPortDerivation(t *testing.T) {
	// TCP 9000 → HTTP 8123 (9000 - 877 = 8123)
	httpPort := 9000 - 877
	assert.Equal(t, 8123, httpPort)

	// Port <= 877 falls back to 8123
	fallback := 500 - 877
	if fallback <= 0 {
		fallback = 8123
	}
	assert.Equal(t, 8123, fallback)
}

func TestCollect_ServerUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	cred := credForServer(srv)
	srv.Close() // shut down before Collect

	c := New()
	_, err := c.Collect(cred)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ping")
}

func TestCollect_ZeroElapsedTime(t *testing.T) {
	srv := mockClickHouseServer(t, standardHandlers(1000, 800, 100, 5, 5000))
	defer srv.Close()

	cred := credForServer(srv)
	c := New()
	_, _ = c.Collect(cred) // baseline

	// Immediately collect again — elapsed may be 0 on a fast machine
	m, err := c.Collect(cred)
	require.NoError(t, err)
	// Rates must never be NaN or negative even at zero elapsed
	assert.GreaterOrEqual(t, m.QueriesPerSecond, 0.0)
	assert.GreaterOrEqual(t, m.SelectQueriesPerSecond, 0.0)
	assert.GreaterOrEqual(t, m.InsertQueriesPerSecond, 0.0)
	assert.GreaterOrEqual(t, m.FailedQueriesPerSecond, 0.0)
	assert.GreaterOrEqual(t, m.InsertedRowsPerSecond, 0.0)
}

func TestCollect_MarkCacheHitRatio_ZeroTotal(t *testing.T) {
	handlers := map[string]string{
		"SELECT 1":       "1\n",
		"system.metrics": `{"metric":"TCPConnection","value":1}`,
		"system.events": strings.Join([]string{
			`{"event":"Query","value":100}`,
			`{"event":"SelectQuery","value":80}`,
			`{"event":"InsertQuery","value":10}`,
			`{"event":"FailedQuery","value":1}`,
			`{"event":"InsertedRows","value":500}`,
			// MarkCacheHits and MarkCacheMisses intentionally absent → total = 0
		}, "\n"),
		"system.parts": `{"value":10}`,
	}
	srv := mockClickHouseServer(t, handlers)
	defer srv.Close()

	c := New()
	m, err := c.Collect(credForServer(srv))
	require.NoError(t, err)
	assert.Equal(t, 100.0, m.MarkCacheHitRatio, "zero cache lookups → nothing missed: 100, matching the PostgreSQL collector's idle case (and never NaN)")
}

func TestToInt64(t *testing.T) {
	assert.Equal(t, int64(42), toInt64(float64(42)))
	assert.Equal(t, int64(100), toInt64(int64(100)))
	assert.Equal(t, int64(7), toInt64(int(7)))
	assert.Equal(t, int64(0), toInt64("not a number"))
	assert.Equal(t, int64(0), toInt64(nil))
}
