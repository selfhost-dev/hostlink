//go:build integration
// +build integration

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

func TestAgentGet_WithExistingAgent(t *testing.T) {
	apiURL, cleanup := startTestAPIWithAgentDetails(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "agent", "get", "agt_123", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agent map[string]any
	err := json.Unmarshal([]byte(stdout), &agent)
	require.NoError(t, err, "Output should be valid JSON")
	assert.Equal(t, "agt_123", agent["id"])
}

func TestAgentGet_WithNonExistentAgent(t *testing.T) {
	apiURL, cleanup := startTestAPIWithAgentDetails(t)
	defer cleanup()

	_, stderr, exitCode := runHlctl(t, "agent", "get", "agt_nonexistent", "--server", apiURL)

	assert.NotEqual(t, 0, exitCode, "Should fail for non-existent agent")
	assert.Contains(t, stderr, "404")
}

func TestAgentGet_WithTags(t *testing.T) {
	apiURL, cleanup := startTestAPIWithAgentDetails(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "agent", "get", "agt_123", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agent map[string]any
	err := json.Unmarshal([]byte(stdout), &agent)
	require.NoError(t, err)

	tags := agent["tags"].([]any)
	assert.NotEmpty(t, tags)
}

func TestAgentGet_WithoutTags(t *testing.T) {
	apiURL, cleanup := startTestAPIWithAgentDetailsNoTags(t)
	defer cleanup()

	stdout, stderr, exitCode := runHlctl(t, "agent", "get", "agt_456", "--server", apiURL)

	assert.Equal(t, 0, exitCode, "stderr: %s", stderr)

	var agent map[string]any
	err := json.Unmarshal([]byte(stdout), &agent)
	require.NoError(t, err)

	tags := agent["tags"].([]any)
	assert.Empty(t, tags)
}

func startTestAPIWithAgentDetails(t *testing.T) (baseURL string, cleanup func()) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v1/agents/agt_123" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"id":            "agt_123",
				"status":        "active",
				"last_seen":     "2025-10-03T00:00:00Z",
				"tags":          []map[string]string{{"key": "env", "value": "prod"}},
				"registered_at": "2025-10-01T00:00:00Z",
			})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/agents/agt_nonexistent" {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "Agent not found"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	return server.URL, server.Close
}

func startTestAPIWithAgentDetailsNoTags(t *testing.T) (baseURL string, cleanup func()) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/api/v1/agents/agt_456" {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"id":            "agt_456",
				"status":        "idle",
				"last_seen":     "2025-10-03T00:00:00Z",
				"tags":          []map[string]string{},
				"registered_at": "2025-10-01T00:00:00Z",
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))

	return server.URL, server.Close
}
