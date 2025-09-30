// Package taskfetcher fetches the tasks from external URL
package taskfetcher

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"hostlink/app/services/agentstate"
	"hostlink/app/services/requestsigner"
	"hostlink/app/services/requestverifier"
	"hostlink/config/appconf"
	"hostlink/db/schema/taskschema"
	"hostlink/domain/nonce"
	rsautil "hostlink/internal/crypto"
	"net/http"
	"time"
)

type TaskFetcher interface {
	Fetch() ([]taskschema.Task, error)
}

type NonceRepository interface {
	Save(ctx context.Context, n *nonce.Nonce) error
	Exists(ctx context.Context, value string) (bool, error)
}

type taskfetcher struct {
	client          *http.Client
	signer          *requestsigner.RequestSigner
	verifier        *requestverifier.RequestVerifier
	nonceRepo       NonceRepository
	controlPlaneURL string
}

type Config struct {
	AgentState      *agentstate.AgentState
	PrivateKeyPath  string
	ControlPlaneURL string
	ServerPublicKey *rsa.PublicKey
	NonceRepo       NonceRepository
	Timeout         time.Duration
}

func New(cfg *Config) (*taskfetcher, error) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	agentID := cfg.AgentState.GetAgentID()
	if agentID == "" {
		return nil, fmt.Errorf("agent not registered: missing agent ID")
	}

	signer, err := requestsigner.New(cfg.PrivateKeyPath, agentID)
	if err != nil {
		return nil, fmt.Errorf("failed to create request signer: %w", err)
	}

	verifier := requestverifier.New(cfg.ServerPublicKey)

	return &taskfetcher{
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		signer:          signer,
		verifier:        verifier,
		nonceRepo:       cfg.NonceRepo,
		controlPlaneURL: cfg.ControlPlaneURL,
	}, nil
}

func NewDefault(nonceRepo NonceRepository) (*taskfetcher, error) {
	agentState := agentstate.New(appconf.AgentStatePath())
	if err := agentState.Load(); err != nil {
		return nil, fmt.Errorf("failed to load agent state: %w", err)
	}

	serverPublicKey, err := loadServerPublicKey(appconf.ServerPublicKeyPath())
	if err != nil {
		return nil, fmt.Errorf("failed to load server public key: %w", err)
	}

	return New(&Config{
		AgentState:      agentState,
		PrivateKeyPath:  appconf.AgentPrivateKeyPath(),
		ControlPlaneURL: appconf.ControlPlaneURL(),
		ServerPublicKey: serverPublicKey,
		NonceRepo:       nonceRepo,
	})
}

func (tf *taskfetcher) Fetch() ([]taskschema.Task, error) {
	ctx := context.Background()
	url := tf.controlPlaneURL + "/api/v1/tasks"

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if err := tf.signer.SignRequest(req); err != nil {
		return nil, fmt.Errorf("failed to sign request: %w", err)
	}

	resp, err := tf.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if err := tf.verifier.VerifyResponse(resp); err != nil {
		return nil, fmt.Errorf("response verification failed: %w", err)
	}

	responseNonce := resp.Header.Get("X-Nonce")
	if tf.nonceRepo != nil {
		exists, err := tf.nonceRepo.Exists(ctx, responseNonce)
		if err != nil {
			return nil, fmt.Errorf("failed to check response nonce: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("duplicate nonce detected: replay attack prevented")
		}

		n := &nonce.Nonce{
			Value:     responseNonce,
			CreatedAt: time.Now(),
		}
		if err := tf.nonceRepo.Save(ctx, n); err != nil {
			return nil, fmt.Errorf("failed to store response nonce: %w", err)
		}
	}

	var tasks []taskschema.Task
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return tasks, nil
}

func loadServerPublicKey(path string) (*rsa.PublicKey, error) {
	publicKey, err := rsautil.LoadPublicKey(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load public key: %w", err)
	}
	return publicKey, nil
}
