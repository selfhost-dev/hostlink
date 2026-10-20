package apiserver

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDo_ServerError_ReportsStatusCode - a server error must name the status
// code instead of reporting an empty error.
func TestDo_ServerError_ReportsStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	c := setupTestClient(t, server.URL)
	resp, err := c.Heartbeat(context.Background(), "test-agent-123")

	require.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "unexpected status code: 500")
}
