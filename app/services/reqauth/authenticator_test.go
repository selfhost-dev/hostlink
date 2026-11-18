package reqauth

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

// #4113: version 2 binds the signature to the request line and body. The
// integration tests exercise it end-to-end (the fetch and the result PUT);
// these are the unit-level contract.
func TestAuthenticateSignatureV2(t *testing.T) {
	t.Run("accepts a version 2 GET", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)
		req := createSignedRequestV2(t, privateKey, http.MethodGet, "/api/v1/tasks?agent=agt_test123", "", "2")

		if err := auth.Authenticate(req); err != nil {
			t.Fatalf("expected the version 2 signature to verify, got: %v", err)
		}
	})

	t.Run("accepts a version 2 PUT and leaves the body readable", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)
		body := `{"status":"completed","exit_code":0}`
		req := createSignedRequestV2(t, privateKey, http.MethodPut, "/api/v1/tasks/tsk_1", body, "2")

		if err := auth.Authenticate(req); err != nil {
			t.Fatalf("expected the version 2 signature to verify, got: %v", err)
		}

		read, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("body must stay readable for the handler: %v", err)
		}
		if string(read) != body {
			t.Fatalf("body after authentication = %q, want %q", read, body)
		}
	})

	t.Run("rejects a signature moved to another path", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)
		req := createSignedRequestV2(t, privateKey, http.MethodPut, "/api/v1/tasks/tsk_1", "{}", "2")
		req.URL.Path = "/api/v1/tasks/tsk_2"

		if err := auth.Authenticate(req); err == nil {
			t.Fatal("a signature for another path must not verify")
		}
	})

	t.Run("rejects a signature with a different body", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)
		req := createSignedRequestV2(t, privateKey, http.MethodPut, "/api/v1/tasks/tsk_1", `{"status":"failed"}`, "2")
		replaced := `{"status":"completed"}`
		req.Body = io.NopCloser(strings.NewReader(replaced))
		req.GetBody = nil

		if err := auth.Authenticate(req); err == nil {
			t.Fatal("a signature for another body must not verify")
		}
	})

	t.Run("accepts an explicit version 1 request", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)
		req, err := createSignedRequest(testRequest{}, privateKey)
		if err != nil {
			t.Fatalf("failed to create signed request: %v", err)
		}
		req.Header.Set(SignatureVersionHeader, "1")

		if err := auth.Authenticate(req); err != nil {
			t.Fatalf("expected the version 1 signature to keep verifying, got: %v", err)
		}
	})

	t.Run("rejects an unknown signature version", func(t *testing.T) {
		auth, privateKey := setupTestAuthenticator(t)
		req, err := createSignedRequest(testRequest{}, privateKey)
		if err != nil {
			t.Fatalf("failed to create signed request: %v", err)
		}
		req.Header.Set(SignatureVersionHeader, "3")

		if err := auth.Authenticate(req); err == nil {
			t.Fatal("an unknown signature version must be refused, not guessed")
		}
	})
}

func createSignedRequestV2(t *testing.T, privateKey *rsa.PrivateKey, method, path, body, version string) *http.Request {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)

	agentID := "agt_test123"
	timestamp := time.Now().Unix()
	nonce := "test-nonce-v2"
	digest := sha256Hex([]byte(body))
	message := fmt.Sprintf("%s|%d|%s|%s|%s|%s", agentID, timestamp, nonce, method, req.URL.Path, digest)
	hashed := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hashed[:], nil)
	if err != nil {
		t.Fatalf("failed to sign request: %v", err)
	}

	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("X-Nonce", nonce)
	req.Header.Set("X-Signature", base64.StdEncoding.EncodeToString(signature))
	req.Header.Set(SignatureVersionHeader, version)

	return req
}
