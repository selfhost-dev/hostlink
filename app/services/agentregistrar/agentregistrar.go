package agentregistrar

import (
	"bytes"
	"encoding/json"
	"fmt"
	"hostlink/config/appconf"
	"hostlink/internal/crypto"
	"net/http"
	"os"
	"time"
)

type Registrar struct {
	client          *http.Client
	controlPlaneURL string
	tokenID         string
	tokenKey        string
	privateKeyPath  string
}

type Config struct {
	ControlPlaneURL string
	TokenID         string
	TokenKey        string
	PrivateKeyPath  string
	Timeout         time.Duration
}

type RegistrationRequest struct {
	Fingerprint   string    `json:"fingerprint"`
	TokenID       string    `json:"token_id"`
	TokenKey      string    `json:"token_key"`
	PublicKey     string    `json:"public_key"`
	PublicKeyType string    `json:"public_key_type"`
	Tags          []TagPair `json:"tags"`
}

type TagPair struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type RegistrationResponse struct {
	ID           string    `json:"id"`
	Fingerprint  string    `json:"fingerprint"`
	Status       string    `json:"status"`
	Message      string    `json:"message"`
	RegisteredAt time.Time `json:"registered_at"`
}

func New() *Registrar {
	return NewWithConfig(&Config{
		ControlPlaneURL: appconf.ControlPlaneURL(),
		TokenID:         appconf.AgentTokenID(),
		TokenKey:        appconf.AgentTokenKey(),
		PrivateKeyPath:  appconf.AgentPrivateKeyPath(),
		Timeout:         30 * time.Second,
	})
}

func NewWithConfig(cfg *Config) *Registrar {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	return &Registrar{
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		controlPlaneURL: cfg.ControlPlaneURL,
		tokenID:         cfg.TokenID,
		tokenKey:        cfg.TokenKey,
		privateKeyPath:  cfg.PrivateKeyPath,
	}
}

func (r *Registrar) Register(fingerprint string, publicKeyBase64 string, tags []TagPair) (*RegistrationResponse, error) {
	if r.tokenID == "" || r.tokenKey == "" {
		return nil, fmt.Errorf("token credentials not configured")
	}

	request := RegistrationRequest{
		Fingerprint:   fingerprint,
		TokenID:       r.tokenID,
		TokenKey:      r.tokenKey,
		PublicKey:     publicKeyBase64,
		PublicKeyType: "RSA",
		Tags:          tags,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := r.controlPlaneURL + "/api/v1/agents/register"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registration request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errorResp map[string]string
		json.NewDecoder(resp.Body).Decode(&errorResp)
		if errMsg, ok := errorResp["error"]; ok {
			return nil, fmt.Errorf("registration failed: %s", errMsg)
		}
		return nil, fmt.Errorf("registration failed with status %d", resp.StatusCode)
	}

	var response RegistrationResponse
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &response, nil
}

func (r *Registrar) PreparePublicKey() (string, error) {
	privateKey, err := crypto.LoadOrGenerateKeypair(r.privateKeyPath, 2048)
	if err != nil {
		return "", fmt.Errorf("failed to load/generate keypair: %w", err)
	}

	publicKeyBase64, err := crypto.GetPublicKeyBase64(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to get public key: %w", err)
	}

	return publicKeyBase64, nil
}

func (r *Registrar) GetDefaultTags() []TagPair {
	hostname, _ := os.Hostname()

	return []TagPair{
		{Key: "hostname", Value: hostname},
		{Key: "os", Value: "linux"},
	}
}