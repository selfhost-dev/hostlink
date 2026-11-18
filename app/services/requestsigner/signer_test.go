package requestsigner

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRequestSigner_New(t *testing.T) {
	t.Run("should load private key from file", func(t *testing.T) {
		tempDir := t.TempDir()
		privateKey := generateTestPrivateKey(t)
		keyPath := saveTestPrivateKey(t, tempDir, privateKey)

		signer, err := New(keyPath, "test-agent-123")

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if signer == nil {
			t.Fatal("expected signer to be created")
		}
		if signer.agentID != "test-agent-123" {
			t.Errorf("expected agent ID 'test-agent-123', got %s", signer.agentID)
		}
		if signer.privateKey == nil {
			t.Error("expected private key to be loaded")
		}
	})

	t.Run("should handle missing private key gracefully", func(t *testing.T) {
		tempDir := t.TempDir()
		nonExistentPath := filepath.Join(tempDir, "nonexistent.key")

		signer, err := New(nonExistentPath, "test-agent-123")

		if err == nil {
			t.Fatal("expected error for missing private key")
		}
		if signer != nil {
			t.Error("expected signer to be nil on error")
		}
	})

	t.Run("should validate private key format", func(t *testing.T) {
		tempDir := t.TempDir()
		invalidKeyPath := filepath.Join(tempDir, "invalid.key")

		if err := os.WriteFile(invalidKeyPath, []byte("invalid key content"), 0600); err != nil {
			t.Fatalf("failed to create invalid key file: %v", err)
		}

		signer, err := New(invalidKeyPath, "test-agent-123")

		if err == nil {
			t.Fatal("expected error for invalid private key format")
		}
		if signer != nil {
			t.Error("expected signer to be nil on error")
		}
	})

	t.Run("should require agent ID", func(t *testing.T) {
		tempDir := t.TempDir()
		privateKey := generateTestPrivateKey(t)
		keyPath := saveTestPrivateKey(t, tempDir, privateKey)

		signer, err := New(keyPath, "")

		if err == nil {
			t.Fatal("expected error for empty agent ID")
		}
		if signer != nil {
			t.Error("expected signer to be nil on error")
		}
	})
}

func TestRequestSigner_SignRequest(t *testing.T) {
	t.Run("should add required headers to request", func(t *testing.T) {
		signer := setupTestSigner(t)
		req := createTestRequest(t, "GET", "https://example.com/api/tasks")

		err := signer.SignRequest(req)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		requiredHeaders := []string{"X-Agent-ID", "X-Timestamp", "X-Nonce", "X-Signature", "X-Agent-Signature-Version"}
		for _, header := range requiredHeaders {
			if req.Header.Get(header) == "" {
				t.Errorf("expected header %s to be set", header)
			}
		}
	})

	t.Run("should add X-Agent-ID header with agent ID", func(t *testing.T) {
		signer := setupTestSigner(t)
		req := createTestRequest(t, "GET", "https://example.com/api/tasks")

		err := signer.SignRequest(req)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		agentID := req.Header.Get("X-Agent-ID")
		if agentID != "test-agent-123" {
			t.Errorf("expected agent ID 'test-agent-123', got %s", agentID)
		}
	})

	t.Run("should add X-Timestamp header with Unix timestamp", func(t *testing.T) {
		signer := setupTestSigner(t)
		req := createTestRequest(t, "GET", "https://example.com/api/tasks")

		before := time.Now().Unix()
		err := signer.SignRequest(req)
		after := time.Now().Unix()

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		timestampStr := req.Header.Get("X-Timestamp")
		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			t.Fatalf("expected valid Unix timestamp, got %s", timestampStr)
		}

		if timestamp < before || timestamp > after {
			t.Errorf("expected timestamp between %d and %d, got %d", before, after, timestamp)
		}
	})

	t.Run("should add X-Nonce header with unique value", func(t *testing.T) {
		signer := setupTestSigner(t)
		req := createTestRequest(t, "GET", "https://example.com/api/tasks")

		err := signer.SignRequest(req)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		nonce := req.Header.Get("X-Nonce")
		if nonce == "" {
			t.Fatal("expected nonce to be set")
		}

		_, err = base64.StdEncoding.DecodeString(nonce)
		if err != nil {
			t.Errorf("expected nonce to be valid base64, got error: %v", err)
		}
	})

	t.Run("should add X-Signature header with base64 encoded signature", func(t *testing.T) {
		signer := setupTestSigner(t)
		req := createTestRequest(t, "GET", "https://example.com/api/tasks")

		err := signer.SignRequest(req)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		signature := req.Header.Get("X-Signature")
		if signature == "" {
			t.Fatal("expected signature to be set")
		}

		_, err = base64.StdEncoding.DecodeString(signature)
		if err != nil {
			t.Errorf("expected signature to be valid base64, got error: %v", err)
		}
	})

	t.Run("should generate unique nonce for each request", func(t *testing.T) {
		signer := setupTestSigner(t)

		req1 := createTestRequest(t, "GET", "https://example.com/api/tasks")
		req2 := createTestRequest(t, "GET", "https://example.com/api/tasks")

		if err := signer.SignRequest(req1); err != nil {
			t.Fatalf("expected no error for req1, got %v", err)
		}
		if err := signer.SignRequest(req2); err != nil {
			t.Fatalf("expected no error for req2, got %v", err)
		}

		nonce1 := req1.Header.Get("X-Nonce")
		nonce2 := req2.Header.Get("X-Nonce")

		if nonce1 == nonce2 {
			t.Errorf("expected unique nonces, got same value: %s", nonce1)
		}
	})
}

func TestRequestSigner_SignHeaders(t *testing.T) {
	t.Run("should return required headers for websocket upgrade", func(t *testing.T) {
		signer := setupTestSigner(t)

		headers, err := signer.SignHeaders("GET", "/agents/socket")

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		requiredHeaders := []string{"X-Agent-ID", "X-Timestamp", "X-Nonce", "X-Signature", "X-Agent-Signature-Version"}
		for _, header := range requiredHeaders {
			if headers.Get(header) == "" {
				t.Errorf("expected header %s to be set", header)
			}
		}
		if headers.Get("X-Agent-ID") != "test-agent-123" {
			t.Errorf("expected X-Agent-ID test-agent-123, got %q", headers.Get("X-Agent-ID"))
		}
	})

	t.Run("should produce signature verifiable from returned headers", func(t *testing.T) {
		signer := setupTestSigner(t)

		headers, err := signer.SignHeaders("GET", "/agents/socket")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		signatureBytes, err := base64.StdEncoding.DecodeString(headers.Get("X-Signature"))
		if err != nil {
			t.Fatalf("expected base64 signature, got %v", err)
		}

		if headers.Get("X-Agent-Signature-Version") != "2" {
			t.Fatalf("expected version 2, got %q", headers.Get("X-Agent-Signature-Version"))
		}
		empty := sha256.Sum256(nil)
		message := fmt.Sprintf("%s|%s|%s|GET|/agents/socket|%s",
			headers.Get("X-Agent-ID"), headers.Get("X-Timestamp"), headers.Get("X-Nonce"),
			hex.EncodeToString(empty[:]))
		hashed := sha256.Sum256([]byte(message))
		if err := rsa.VerifyPSS(&signer.privateKey.PublicKey, crypto.SHA256, hashed[:], signatureBytes, nil); err != nil {
			t.Errorf("signature verification failed: %v", err)
		}
	})
}

// #4113: the signature covers the method, path and body, so it cannot be
// replayed against another endpoint of the same agent.
func TestRequestSigner_SignRequestBindsTheRequest(t *testing.T) {
	signer := setupTestSigner(t)
	body := `{"task_id":"tsk_1"}`

	req, err := http.NewRequest("POST", "https://example.com/agents/v1/tasks/tsk_1/result", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if err := signer.SignRequest(req); err != nil {
		t.Fatalf("sign request: %v", err)
	}

	// The body is not consumed by signing.
	sent, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(sent) != body {
		t.Fatalf("body changed while signing: %q", string(sent))
	}

	digest := sha256.Sum256([]byte(body))
	message := fmt.Sprintf("%s|%s|%s|POST|/agents/v1/tasks/tsk_1/result|%s",
		req.Header.Get("X-Agent-ID"), req.Header.Get("X-Timestamp"), req.Header.Get("X-Nonce"),
		hex.EncodeToString(digest[:]))
	hashed := sha256.Sum256([]byte(message))
	signature, err := base64.StdEncoding.DecodeString(req.Header.Get("X-Signature"))
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if err := rsa.VerifyPSS(&signer.privateKey.PublicKey, crypto.SHA256, hashed[:], signature, nil); err != nil {
		t.Fatalf("signature verification failed: %v", err)
	}

	// The same signature does not verify for a different path.
	other := strings.Replace(message, "/agents/v1/tasks/tsk_1/result", "/agents/v1/heartbeats/tsk_1", 1)
	otherHash := sha256.Sum256([]byte(other))
	if err := rsa.VerifyPSS(&signer.privateKey.PublicKey, crypto.SHA256, otherHash[:], signature, nil); err == nil {
		t.Fatal("the signature must not verify on another path")
	}
}

const emptyBodyDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func TestRequestSigner_GenerateSignature(t *testing.T) {
	t.Run("should generate valid RSA-PSS signature", func(t *testing.T) {
		signer := setupTestSigner(t)

		signature, err := signer.generateSignature("test-agent-123", "1234567890", "test-nonce", "POST", "/agents/v1/tasks", emptyBodyDigest)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if signature == "" {
			t.Fatal("expected signature to be generated")
		}

		_, err = base64.StdEncoding.DecodeString(signature)
		if err != nil {
			t.Errorf("expected signature to be valid base64, got error: %v", err)
		}
	})

	t.Run("should use SHA-256 hash algorithm", func(t *testing.T) {
		signer := setupTestSigner(t)

		agentID := "test-agent-123"
		timestamp := "1234567890"
		nonce := "test-nonce"

		signatureBase64, err := signer.generateSignature(agentID, timestamp, nonce, "POST", "/agents/v1/tasks", emptyBodyDigest)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		signatureBytes, _ := base64.StdEncoding.DecodeString(signatureBase64)
		message := fmt.Sprintf("%s|%s|%s|POST|/agents/v1/tasks|%s", agentID, timestamp, nonce, emptyBodyDigest)
		hashed := sha256.Sum256([]byte(message))

		err = rsa.VerifyPSS(&signer.privateKey.PublicKey, crypto.SHA256, hashed[:], signatureBytes, nil)
		if err != nil {
			t.Errorf("signature verification with SHA-256 failed: %v", err)
		}
	})

	t.Run("should sign the version 2 message AID|timestamp|nonce|method|path|body", func(t *testing.T) {
		signer := setupTestSigner(t)

		agentID := "agent-456"
		timestamp := "9876543210"
		nonce := "unique-nonce"

		signatureBase64, err := signer.generateSignature(agentID, timestamp, nonce, "POST", "/agents/v1/tasks", emptyBodyDigest)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		signatureBytes, _ := base64.StdEncoding.DecodeString(signatureBase64)
		expectedMessage := "agent-456|9876543210|unique-nonce|POST|/agents/v1/tasks|" + emptyBodyDigest
		hashed := sha256.Sum256([]byte(expectedMessage))

		err = rsa.VerifyPSS(&signer.privateKey.PublicKey, crypto.SHA256, hashed[:], signatureBytes, nil)
		if err != nil {
			t.Errorf("signature verification failed, message format may be incorrect: %v", err)
		}
	})

	t.Run("should produce verifiable signatures", func(t *testing.T) {
		signer := setupTestSigner(t)

		agentID := "test-agent-123"
		timestamp := "1234567890"
		nonce := "test-nonce"

		signatureBase64, err := signer.generateSignature(agentID, timestamp, nonce, "POST", "/agents/v1/tasks", emptyBodyDigest)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		signatureBytes, err := base64.StdEncoding.DecodeString(signatureBase64)
		if err != nil {
			t.Fatalf("failed to decode signature: %v", err)
		}

		message := fmt.Sprintf("%s|%s|%s|POST|/agents/v1/tasks|%s", agentID, timestamp, nonce, emptyBodyDigest)
		hashed := sha256.Sum256([]byte(message))

		err = rsa.VerifyPSS(&signer.privateKey.PublicKey, crypto.SHA256, hashed[:], signatureBytes, nil)
		if err != nil {
			t.Errorf("signature verification failed: %v", err)
		}
	})

	t.Run("should produce different signatures for different inputs", func(t *testing.T) {
		signer := setupTestSigner(t)

		sig1, err := signer.generateSignature("agent-1", "1111111111", "nonce-1", "POST", "/a", emptyBodyDigest)
		if err != nil {
			t.Fatalf("expected no error for sig1, got %v", err)
		}

		sig2, err := signer.generateSignature("agent-2", "2222222222", "nonce-2", "POST", "/b", emptyBodyDigest)
		if err != nil {
			t.Fatalf("expected no error for sig2, got %v", err)
		}

		if sig1 == sig2 {
			t.Error("expected different signatures for different inputs")
		}
	})
}

func TestRequestSigner_GenerateNonce(t *testing.T) {
	t.Run("should generate 16 byte random nonce", func(t *testing.T) {
		signer := setupTestSigner(t)

		nonce, err := signer.generateNonce()

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		decodedBytes, err := base64.StdEncoding.DecodeString(nonce)
		if err != nil {
			t.Fatalf("expected valid base64, got error: %v", err)
		}

		if len(decodedBytes) != 16 {
			t.Errorf("expected 16 bytes, got %d bytes", len(decodedBytes))
		}
	})

	t.Run("should encode nonce as base64", func(t *testing.T) {
		signer := setupTestSigner(t)

		nonce, err := signer.generateNonce()

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		_, err = base64.StdEncoding.DecodeString(nonce)
		if err != nil {
			t.Errorf("expected valid base64 encoding, got error: %v", err)
		}
	})

	t.Run("should use cryptographically secure random", func(t *testing.T) {
		signer := setupTestSigner(t)

		nonces := make(map[string]bool)
		iterations := 1000

		for range iterations {
			nonce, err := signer.generateNonce()
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if nonces[nonce] {
				t.Errorf("duplicate nonce found: %s", nonce)
			}
			nonces[nonce] = true
		}

		if len(nonces) != iterations {
			t.Errorf("expected %d unique nonces, got %d", iterations, len(nonces))
		}
	})
}

// Helper functions for tests
func setupTestSigner(t *testing.T) *RequestSigner {
	t.Helper()

	tempDir := t.TempDir()
	privateKey := generateTestPrivateKey(t)
	keyPath := saveTestPrivateKey(t, tempDir, privateKey)

	signer, err := New(keyPath, "test-agent-123")
	if err != nil {
		t.Fatalf("failed to create test signer: %v", err)
	}

	return signer
}

func generateTestPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test private key: %v", err)
	}

	return privateKey
}

func createTestRequest(t *testing.T, method, url string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatalf("failed to create test request: %v", err)
	}

	return req
}

func saveTestPrivateKey(t *testing.T, dir string, key *rsa.PrivateKey) string {
	t.Helper()

	keyPath := filepath.Join(dir, "test-agent.key")

	privateKeyBytes := x509.MarshalPKCS1PrivateKey(key)
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}

	file, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("failed to create key file: %v", err)
	}
	defer file.Close()

	if err := pem.Encode(file, privateKeyPEM); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	return keyPath
}
