package client

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client interface for interacting with the Hostlink API
type Client interface {
	CreateTask(req *CreateTaskRequest) (*CreateTaskResponse, error)
	ListAgents(tags []string) ([]Agent, error)
	ListTasks(filters *ListTasksRequest) ([]Task, error)
	GetTask(taskID string) (*TaskDetails, error)
}

// HTTPClient implements the Client interface
type HTTPClient struct {
	baseURL string
	client  *http.Client
}

// NewHTTPClient creates a new HTTP client
func NewHTTPClient(baseURL string) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// CreateTaskRequest represents the request payload for creating a task
type CreateTaskRequest struct {
	Command  string   `json:"command"`
	Priority int      `json:"priority"`
	AgentIDs []string `json:"agent_ids,omitempty"`
}

// CreateTaskResponse represents the response from creating a task
type CreateTaskResponse struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

// Agent represents an agent from the API
type Agent struct {
	ID       string    `json:"id"`
	Status   string    `json:"status"`
	LastSeen time.Time `json:"last_seen"`
	Tags     []Tag     `json:"tags"`
}

// Tag represents a tag key-value pair
type Tag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ListTasksRequest represents filters for listing tasks
type ListTasksRequest struct {
	Status  string
	AgentID string
}

// Task represents a task from the API
type Task struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	Status    string    `json:"status"`
	Priority  int       `json:"priority"`
	CreatedAt time.Time `json:"created_at"`
}

// TaskDetails represents full task details from the API
type TaskDetails struct {
	ID          string     `json:"id"`
	Command     string     `json:"command"`
	Status      string     `json:"status"`
	Priority    int        `json:"priority"`
	AgentID     *string    `json:"agent_id"`
	Output      *string    `json:"output"`
	ExitCode    *int       `json:"exit_code"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at"`
	CompletedAt *time.Time `json:"completed_at"`
}

// CreateTask creates a new task via the API
func (c *HTTPClient) CreateTask(req *CreateTaskRequest) (*CreateTaskResponse, error) {
	jsonData, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest("POST", c.baseURL+"/api/v2/tasks", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var taskResp CreateTaskResponse
	if err := json.NewDecoder(resp.Body).Decode(&taskResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &taskResp, nil
}

// ListAgents lists agents, optionally filtered by tags
func (c *HTTPClient) ListAgents(tags []string) ([]Agent, error) {
	u, err := url.Parse(c.baseURL + "/api/v1/agents")
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	q := u.Query()
	for _, tag := range tags {
		q.Add("tag", tag)
	}
	u.RawQuery = q.Encode()

	resp, err := c.client.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var agents []Agent
	if err := json.NewDecoder(resp.Body).Decode(&agents); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return agents, nil
}

// ListTasks lists tasks with optional filters
func (c *HTTPClient) ListTasks(filters *ListTasksRequest) ([]Task, error) {
	u, err := url.Parse(c.baseURL + "/api/v2/tasks")
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	if filters != nil {
		q := u.Query()
		if filters.Status != "" {
			q.Add("status", filters.Status)
		}
		if filters.AgentID != "" {
			q.Add("agent", filters.AgentID)
		}
		u.RawQuery = q.Encode()
	}

	resp, err := c.client.Get(u.String())
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var tasks []Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return tasks, nil
}

// GetTask gets task details by ID
func (c *HTTPClient) GetTask(taskID string) (*TaskDetails, error) {
	url := fmt.Sprintf("%s/api/v2/tasks/%s", c.baseURL, taskID)

	resp, err := c.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API error: status %d, body: %s", resp.StatusCode, string(body))
	}

	var task TaskDetails
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &task, nil
}
