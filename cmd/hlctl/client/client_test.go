package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHTTPClient(t *testing.T) {
	client := NewHTTPClient("http://localhost:8080")

	require.NotNil(t, client)
	assert.Equal(t, "http://localhost:8080", client.baseURL)
}

func TestCreateTask_SendsCorrectRequest(t *testing.T) {
	var capturedRequest *CreateTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v2/tasks", r.URL.Path)
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		json.NewDecoder(r.Body).Decode(&capturedRequest)

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(CreateTaskResponse{
			ID:     "task-123",
			Status: "pending",
		})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL)
	req := &CreateTaskRequest{
		Command:  "ls -la",
		Priority: 2,
	}

	_, err := client.CreateTask(req)

	require.NoError(t, err)
	assert.Equal(t, "ls -la", capturedRequest.Command)
	assert.Equal(t, 2, capturedRequest.Priority)
}

func TestCreateTask_ParsesSuccessResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         "task-456",
			"status":     "pending",
			"created_at": "2025-10-03T00:00:00Z",
		})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL)
	req := &CreateTaskRequest{Command: "echo hello", Priority: 1}

	resp, err := client.CreateTask(req)

	require.NoError(t, err)
	assert.Equal(t, "task-456", resp.ID)
	assert.Equal(t, "pending", resp.Status)
	assert.False(t, resp.CreatedAt.IsZero())
}

func TestCreateTask_HandlesAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid command",
		})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL)
	req := &CreateTaskRequest{Command: "", Priority: 1}

	_, err := client.CreateTask(req)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400")
}

func TestCreateTask_HandlesNetworkError(t *testing.T) {
	client := NewHTTPClient("http://localhost:99999")
	req := &CreateTaskRequest{Command: "ls", Priority: 1}

	_, err := client.CreateTask(req)

	require.Error(t, err)
}

func TestCreateTask_IncludesAgentIDs(t *testing.T) {
	var capturedRequest *CreateTaskRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedRequest)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(CreateTaskResponse{ID: "task-1", Status: "pending"})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL)
	req := &CreateTaskRequest{
		Command:  "ls",
		Priority: 1,
		AgentIDs: []string{"agt_1", "agt_2"},
	}

	_, err := client.CreateTask(req)

	require.NoError(t, err)
	assert.Equal(t, []string{"agt_1", "agt_2"}, capturedRequest.AgentIDs)
}

func TestCreateTask_OmitsEmptyAgentIDs(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&capturedBody)
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(CreateTaskResponse{ID: "task-1", Status: "pending"})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL)
	req := &CreateTaskRequest{
		Command:  "ls",
		Priority: 1,
		AgentIDs: []string{},
	}

	_, err := client.CreateTask(req)

	require.NoError(t, err)
	_, hasAgentIDs := capturedBody["agent_ids"]
	assert.False(t, hasAgentIDs, "agent_ids should be omitted when empty")
}

func TestListAgents_WithoutTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/agents", r.URL.Path)
		assert.Equal(t, "GET", r.Method)
		assert.Empty(t, r.URL.Query().Get("tag"))

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]Agent{
			{ID: "agt_1"},
			{ID: "agt_2"},
		})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL)

	agents, err := client.ListAgents([]string{})

	require.NoError(t, err)
	assert.Len(t, agents, 2)
	assert.Equal(t, "agt_1", agents[0].ID)
}

func TestListAgents_WithSingleTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/agents", r.URL.Path)
		tags := r.URL.Query()["tag"]
		assert.Equal(t, []string{"env:prod"}, tags)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]Agent{{ID: "agt_1"}})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL)

	agents, err := client.ListAgents([]string{"env:prod"})

	require.NoError(t, err)
	assert.Len(t, agents, 1)
}

func TestListAgents_WithMultipleTags(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tags := r.URL.Query()["tag"]
		assert.ElementsMatch(t, []string{"env:prod", "region:us"}, tags)

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]Agent{{ID: "agt_1"}})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL)

	agents, err := client.ListAgents([]string{"env:prod", "region:us"})

	require.NoError(t, err)
	assert.Len(t, agents, 1)
}

func TestListAgents_ParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode([]Agent{
			{ID: "agt_100"},
			{ID: "agt_200"},
			{ID: "agt_300"},
		})
	}))
	defer server.Close()

	client := NewHTTPClient(server.URL)

	agents, err := client.ListAgents([]string{})

	require.NoError(t, err)
	require.Len(t, agents, 3)
	assert.Equal(t, "agt_100", agents[0].ID)
	assert.Equal(t, "agt_200", agents[1].ID)
	assert.Equal(t, "agt_300", agents[2].ID)
}
