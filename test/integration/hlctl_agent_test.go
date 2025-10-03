package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentList_WithoutFilters(t *testing.T) {
	apiURL, cleanup := startTestAPIWithAgents(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "agent", "list", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agents []map[string]any
	err := json.Unmarshal([]byte(stdout), &agents)
	require.NoError(t, err, "Output should be valid JSON array")
	assert.Len(t, agents, 2)
}

func TestAgentList_WhenNoAgentsExist(t *testing.T) {
	apiURL, cleanup := startTestAPIWithEmptyAgents(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "agent", "list", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agents []map[string]any
	err := json.Unmarshal([]byte(stdout), &agents)
	require.NoError(t, err, "Output should be valid JSON array")
	assert.Empty(t, agents)
}

func TestAgentList_VerifyTagsIncluded(t *testing.T) {
	apiURL, cleanup := startTestAPIWithAgents(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "agent", "list", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agents []map[string]any
	err := json.Unmarshal([]byte(stdout), &agents)
	require.NoError(t, err)

	assert.NotEmpty(t, agents[0]["tags"])
}

func startTestAPIWithAgents(t *testing.T) (baseURL string, cleanup func()) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v1/agents" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"id":        "agt_1",
					"status":    "active",
					"last_seen": "2025-10-03T00:00:00Z",
					"tags":      []map[string]string{{"key": "env", "value": "prod"}},
				},
				{
					"id":        "agt_2",
					"status":    "idle",
					"last_seen": "2025-10-03T00:01:00Z",
					"tags":      []map[string]string{{"key": "region", "value": "us"}},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	return server.URL, server.Close
}

func startTestAPIWithEmptyAgents(t *testing.T) (baseURL string, cleanup func()) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v1/agents" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	return server.URL, server.Close
}
