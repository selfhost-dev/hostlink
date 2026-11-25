package sqlstream

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"

	"hostlink/domain/credential"
	"hostlink/internal/apiserver"
	"hostlink/internal/crypto"
)

// apiClient is hostlink's signed client to the backend (internal/apiserver).
type apiClient interface {
	Get(ctx context.Context, path string, result any) error
	Post(ctx context.Context, path string, body any, result any) error
	GetMetricsCreds(ctx context.Context, agentID string) ([]credential.Credential, error)
}

// BackendAPI is API over the agent's signed requests.
type BackendAPI struct {
	client     apiClient
	agentID    string
	privateKey *rsa.PrivateKey
}

// NewBackendAPI returns the backend as the query service needs it.
func NewBackendAPI(client apiClient, agentID string, privateKey *rsa.PrivateKey) *BackendAPI {
	return &BackendAPI{client: client, agentID: agentID, privateKey: privateKey}
}

// Session asks for a relay token: GET /api/v1/agents/:agent_id/sql_stream/session. A 404 is the
// path being off.
func (a *BackendAPI) Session(ctx context.Context) (*Session, error) {
	var s Session
	err := a.client.Get(ctx, fmt.Sprintf("/api/v1/agents/%s/sql_stream/session", a.agentID), &s)
	var apiErr *apiserver.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		return nil, ErrDisabled
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// Outcome reports a run: POST /api/v1/agents/:agent_id/sql_runs/:run_pid/outcome.
func (a *BackendAPI) Outcome(ctx context.Context, runPid string, o Outcome) error {
	return a.client.Post(ctx, fmt.Sprintf("/api/v1/agents/%s/sql_runs/%s/outcome", a.agentID, runPid), o, nil)
}

// PlatformLogin is the platform's own ClickHouse login, the one the agent's metrics use.
func (a *BackendAPI) PlatformLogin(ctx context.Context) (string, string, error) {
	creds, err := a.client.GetMetricsCreds(ctx, a.agentID)
	if err != nil {
		return "", "", err
	}
	for _, c := range creds {
		if c.Dialect != "clickhouse" || c.IsCoolify() || c.PasswdEnc == "" {
			continue
		}
		password, err := crypto.DecryptWithPrivateKey(c.PasswdEnc, a.privateKey)
		if err != nil {
			return "", "", err
		}
		return c.Username, password, nil
	}
	return "", "", errors.New("no ClickHouse login")
}
