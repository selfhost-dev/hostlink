package reqauth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestAuthenticate(t *testing.T) {
	t.Run("successfully authenticates valid request", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)

		req, err := createSignedRequest(testRequest{}, privateKey)
		if err != nil {
			t.Fatalf("Failed to create signed request: %v", err)
		}

		err = auth.Authenticate(req)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
	})

	t.Run("returns error when X-Agent-ID header is missing", func(t *testing.T) {
		auth, _ := setupTestAuthenticator(t)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		req.Header.Set("X-Nonce", "test-nonce")
		req.Header.Set("X-Signature", "test-signature")

		err := auth.Authenticate(req)
		if err == nil {
			t.Error("Expected error for missing X-Agent-ID, got nil")
		}
	})

	t.Run("returns error when X-Timestamp header is missing", func(t *testing.T) {
		auth, _ := setupTestAuthenticator(t)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Agent-ID", "agt_test123")
		req.Header.Set("X-Nonce", "test-nonce")
		req.Header.Set("X-Signature", "test-signature")

		err := auth.Authenticate(req)
		if err == nil {
			t.Error("Expected error for missing X-Timestamp, got nil")
		}
	})

	t.Run("returns error when X-Nonce header is missing", func(t *testing.T) {
		auth, _ := setupTestAuthenticator(t)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Agent-ID", "agt_test123")
		req.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		req.Header.Set("X-Signature", "test-signature")

		err := auth.Authenticate(req)
		if err == nil {
			t.Error("Expected error for missing X-Nonce, got nil")
		}
	})

	t.Run("returns error when X-Signature header is missing", func(t *testing.T) {
		auth, _ := setupTestAuthenticator(t)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Agent-ID", "agt_test123")
		req.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		req.Header.Set("X-Nonce", "test-nonce")

		err := auth.Authenticate(req)
		if err == nil {
			t.Error("Expected error for missing X-Signature, got nil")
		}
	})

	t.Run("returns error when timestamp is too old", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)

		oldTimestamp := time.Now().Unix() - 400 // 400 seconds ago (> 5 minutes)
		req, err := createSignedRequest(testRequest{timestamp: oldTimestamp}, privateKey)
		if err != nil {
			t.Fatalf("Failed to create signed request: %v", err)
		}

		err = auth.Authenticate(req)
		if err == nil {
			t.Error("Expected error for timestamp too old, got nil")
		}
	})

	t.Run("returns error when timestamp is too far in future", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)

		futureTimestamp := time.Now().Unix() + 400 // 400 seconds in future (> 5 minutes)
		req, err := createSignedRequest(testRequest{timestamp: futureTimestamp}, privateKey)
		if err != nil {
			t.Fatalf("Failed to create signed request: %v", err)
		}

		err = auth.Authenticate(req)
		if err == nil {
			t.Error("Expected error for timestamp too far in future, got nil")
		}
	})

	t.Run("returns error when signature is invalid", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)

		req, err := createSignedRequest(testRequest{signature: "invalid-signature-base64"}, privateKey)
		if err != nil {
			t.Fatalf("Failed to create signed request: %v", err)
		}

		err = auth.Authenticate(req)
		if err == nil {
			t.Error("Expected error for invalid signature, got nil")
		}
	})
}

type testRequest struct {
	agentID   string
	timestamp int64
	nonce     string
	signature string
}

func setupTestAuthenticator(t *testing.T) (*Authenticator, *rsa.PrivateKey) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}
	return New(&privateKey.PublicKey), privateKey
}

func createSignedRequest(tr testRequest, privateKey *rsa.PrivateKey) (*http.Request, error) {
	if tr.agentID == "" {
		tr.agentID = "agt_test123"
	}
	if tr.timestamp == 0 {
		tr.timestamp = time.Now().Unix()
	}
	if tr.nonce == "" {
		tr.nonce = "test-nonce-123"
	}

	message := fmt.Sprintf("%s|%d|%s", tr.agentID, tr.timestamp, tr.nonce)
	hashed := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hashed[:], nil)
	if err != nil {
		return nil, err
	}

	if tr.signature == "" {
		tr.signature = base64.StdEncoding.EncodeToString(signature)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Agent-ID", tr.agentID)
	req.Header.Set("X-Timestamp", strconv.FormatInt(tr.timestamp, 10))
	req.Header.Set("X-Nonce", tr.nonce)
	req.Header.Set("X-Signature", tr.signature)

	return req, nil
}
