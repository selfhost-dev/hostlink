// Package apiserver commincates with the apiserver
package apiserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hostlink/app/services/agentstate"
	"hostlink/app/services/requestsigner"
	"hostlink/config/appconf"
	"io"
	"net/http"
	"time"
)

type client struct {
	httpClient *http.Client
	signer     *requestsigner.RequestSigner
	agentState *agentstate.AgentState
	baseURL    string
	maxRetries int
}

func NewClient(cfg Config) (*client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	signer, err := requestsigner.New2(cfg.privateKeyPath, cfg.agentStatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create request signer: %w", err)
	}

	return &client{
		httpClient: &http.Client{
			Timeout: cfg.timeout,
		},
		signer:     signer,
		baseURL:    cfg.baseURL,
		maxRetries: cfg.maxRetries,
	}, nil
}

func NewDefaultClient() (*client, error) {
	return NewClient(Config{
		baseURL:        appconf.ControlPlaneURL(),
		privateKeyPath: appconf.AgentPrivateKeyPath(),
		agentStatePath: appconf.AgentStatePath(),
		timeout:        30 * time.Second,
	})
}

func (c *client) do(ctx context.Context, method, path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(jsonData)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if err := c.signer.SignRequest(req); err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}

	resp, err := c.executeWithRetry(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return c.handleErrorResponse(resp)
	}

	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

func (c *client) executeWithRetry(req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			time.Sleep(time.Duration(attempt) * time.Second)
		}

		resp, err := c.httpClient.Do(req)
		if err == nil && resp.StatusCode < 500 {
			return resp, nil
		}

		lastErr = err
		if resp != nil && resp.StatusCode < 500 {
			return resp, nil
		}
	}
	return nil, fmt.Errorf("request failed after %d retries: %w", c.maxRetries, lastErr)
}

func (c *client) Get(ctx context.Context, path string, result any) error {
	return c.do(ctx, http.MethodGet, path, nil, result)
}

func (c *client) Post(ctx context.Context, path string, body any, result any) error {
	return c.do(ctx, http.MethodPost, path, body, result)
}

func (c *client) Put(ctx context.Context, path string, body any, result any) error {
	return c.do(ctx, http.MethodPut, path, body, result)
}

func (c *client) Delete(ctx context.Context, path string) error {
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

func (c *client) handleErrorResponse(resp *http.Response) error {
	bodyBytes, _ := io.ReadAll(resp.Body)
	return &APIError{
		StatusCode: resp.StatusCode,
		Message:    string(bodyBytes),
	}
}
