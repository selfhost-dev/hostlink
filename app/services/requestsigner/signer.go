package requestsigner

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hostlink/app/services/agentstate"
	"hostlink/config/appconf"
	rsautil "hostlink/internal/crypto"
	"io"
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

// SignRequest signs the request it is attached to. Version 2 binds the
// signature to the method, path and body (#4113), so the signature cannot be
// moved to another endpoint.
func (s *RequestSigner) SignRequest(req *http.Request) error {
	digest, err := bodyDigest(req)
	if err != nil {
		return fmt.Errorf("failed to read request body: %w", err)
	}

	headers, err := s.signHeaders(req.Method, req.URL.Path, digest)
	if err != nil {
		return err
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}

	return nil
}

// SignHeaders signs a request with no body, for a client that cannot hand over
// an *http.Request (the WebSocket upgrade). method and path are the request
// line the control plane will verify.
func (s *RequestSigner) SignHeaders(method, path string) (http.Header, error) {
	return s.signHeaders(method, path, sha256Hex(nil))
}

func (s *RequestSigner) signHeaders(method, path, bodyDigest string) (http.Header, error) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonceValue, err := s.generateNonce()
	if err != nil {
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	signature, err := s.generateSignature(s.agentID, timestamp, nonceValue, method, path, bodyDigest)
	if err != nil {
		return nil, fmt.Errorf("failed to generate signature: %w", err)
	}

	headers := http.Header{}
	headers.Set("X-Agent-ID", s.agentID)
	headers.Set("X-Timestamp", timestamp)
	headers.Set("X-Nonce", nonceValue)
	headers.Set("X-Signature", signature)
	headers.Set("X-Agent-Signature-Version", "2")

	return headers, nil
}

// bodyDigest is the hex SHA-256 of the exact bytes the server will receive.
// It restores req.Body, so signing does not consume what is about to be sent.
func bodyDigest(req *http.Request) (string, error) {
	if req.Body == nil {
		return sha256Hex(nil), nil
	}

	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return "", err
		}
		defer body.Close()
		data, err := io.ReadAll(body)
		if err != nil {
			return "", err
		}
		return sha256Hex(data), nil
	}

	data, err := io.ReadAll(req.Body)
	if err != nil {
		return "", err
	}
	req.Body = io.NopCloser(bytes.NewReader(data))
	return sha256Hex(data), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s *RequestSigner) generateSignature(agentID, timestamp, nonce, method, path, bodyDigest string) (string, error) {
	message := fmt.Sprintf("%s|%s|%s|%s|%s|%s", agentID, timestamp, nonce, method, path, bodyDigest)
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
