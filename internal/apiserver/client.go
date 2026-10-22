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
	"hostlink/internal/httpclient"
	"hostlink/internal/slowrequest"
	"io"
	"net/http"
	"time"
)

type client struct {
	httpClient   *http.Client
	signer       *requestsigner.RequestSigner
	agentState   *agentstate.AgentState
	baseURL      string
	maxRetries   int
	slowReporter slowrequest.Reporter
}

func NewClient(cfg Config) (*client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	signer, err := requestsigner.New2(cfg.privateKeyPath, cfg.agentStatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create request signer: %w", err)
	}

	transport := cfg.transport
	if transport == nil {
		transport = httpclient.SharedControlPlaneTransport()
	}

	return &client{
		httpClient: &http.Client{
			Timeout:   cfg.timeout,
			Transport: transport,
		},
		signer:       signer,
		baseURL:      cfg.baseURL,
		maxRetries:   cfg.maxRetries,
		slowReporter: slowrequest.New(cfg.slowRequestThreshold),
	}, nil
}

func NewDefaultClient() (*client, error) {
	return newControlPlaneClient(30 * time.Second)
}

// NewHeartbeatClient returns a client that uses the dedicated heartbeat
// timeout, which is shorter than the default control-plane timeout.
func NewHeartbeatClient() (*client, error) {
	return newControlPlaneClient(appconf.APIHeartbeatTimeout())
}

// newControlPlaneClient builds a client for the control plane with the given
// request timeout. Every control-plane request is measured against the
// slow-request threshold, so a transport stall is reported instead of being
// silent.
func newControlPlaneClient(timeout time.Duration) (*client, error) {
	return NewClient(Config{
		baseURL:              appconf.ControlPlaneURL(),
		privateKeyPath:       appconf.AgentPrivateKeyPath(),
		agentStatePath:       appconf.AgentStatePath(),
		timeout:              timeout,
		slowRequestThreshold: appconf.APISlowRequestThreshold(),
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
	// The caller may impose its own deadline: the transport needs to know it,
	// so that a caller that gives up is not read as a dead connection.
	req, err := http.NewRequestWithContext(httpclient.WithCallerDeadline(ctx), method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if err := c.signer.SignRequest(req); err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}

	// The measurement covers the request execution only. With request-level
	// retries enabled it would cover every attempt, but retries are off: the
	// job loop is the retry.
	started := time.Now()
	err = c.execute(req, result)
	elapsed := time.Since(started)

	return c.slowReporter.Report(req, elapsed, err)
}

// execute performs the HTTP round trip, the status handling and the response
// decoding of a request.
func (c *client) execute(req *http.Request, result any) error {
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
		if err == nil {
			if resp.StatusCode < 500 {
				return resp, nil
			}
			// The server answered with an error status. The response is not
			// handed to the caller, so its body is closed here.
			err = fmt.Errorf("unexpected status code: %d", resp.StatusCode)
			closeBody(resp)
		}

		lastErr = err
	}
	return nil, fmt.Errorf("request failed after %d retries: %w", c.maxRetries, lastErr)
}

// closeBody drains and closes a response body that is not handed to the
// caller, so the connection can be reused instead of leaked.
func closeBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
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
