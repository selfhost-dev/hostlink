package requestsigner

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"hostlink/app/services/agentstate"
	"hostlink/config/appconf"
	rsautil "hostlink/internal/crypto"
	"net/http"
	"strconv"
	"sync"
	"time"
)

var mu sync.RWMutex

type RequestSigner struct {
	privateKey *rsa.PrivateKey
	agentID    string
}

func New(privateKeyPath, agentID string) (*RequestSigner, error) {
	mu.RLock()
	defer mu.RUnlock()
	if agentID == "" {
		return nil, fmt.Errorf("agent ID is required")
	}

	privateKey, err := rsautil.LoadPrivateKey(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load private key: %w", err)
	}

	return &RequestSigner{
		privateKey: privateKey,
		agentID:    agentID,
	}, nil
}

func New2(privateKeyPath, agentStatePath string) (*RequestSigner, error) {
	mu.RLock()
	defer mu.RUnlock()
	agentState := agentstate.New(appconf.AgentStatePath())
	if err := agentState.Load(); err != nil {
		return nil, fmt.Errorf("failed to load agent state: %w", err)
	}
	agentID := agentState.GetAgentID()
	if agentID == "" {
		return nil, fmt.Errorf("agent ID is required")
	}

	privateKey, err := rsautil.LoadPrivateKey(privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load private key: %w", err)
	}

	return &RequestSigner{
		privateKey: privateKey,
		agentID:    agentID,
	}, nil
}

func (s *RequestSigner) SignRequest(req *http.Request) error {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonceValue, err := s.generateNonce()
	if err != nil {
		return fmt.Errorf("failed to generate nonce: %w", err)
	}

	signature, err := s.generateSignature(s.agentID, timestamp, nonceValue)
	if err != nil {
		return fmt.Errorf("failed to generate signature: %w", err)
	}

	req.Header.Set("X-Agent-ID", s.agentID)
	req.Header.Set("X-Timestamp", timestamp)
	req.Header.Set("X-Nonce", nonceValue)
	req.Header.Set("X-Signature", signature)

	return nil
}

func (s *RequestSigner) generateSignature(agentID, timestamp, nonce string) (string, error) {
	message := fmt.Sprintf("%s|%s|%s", agentID, timestamp, nonce)
	hashed := sha256.Sum256([]byte(message))

	signature, err := rsa.SignPSS(rand.Reader, s.privateKey, crypto.SHA256, hashed[:], nil)
	if err != nil {
		return "", fmt.Errorf("failed to sign message: %w", err)
	}

	return base64.StdEncoding.EncodeToString(signature), nil
}

func (s *RequestSigner) generateNonce() (string, error) {
	bytes := make([]byte, 16)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	return base64.StdEncoding.EncodeToString(bytes), nil
}
