package requestverifier

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestRequestVerifier_New(t *testing.T) {
	t.Run("should create verifier with public key", func(t *testing.T) {
		publicKey := generateTestPublicKey(t)

		verifier := New(publicKey)

		if verifier == nil {
			t.Fatal("expected verifier to be created")
		}
		if verifier.publicKey == nil {
			t.Error("expected public key to be set")
		}
	})
}

func TestRequestVerifier_VerifyResponse(t *testing.T) {
	t.Run("should verify valid response", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		resp := createSignedResponse(t, privateKey, "server-123", time.Now().Unix())

		err := verifier.VerifyResponse(resp)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("should reject response with missing X-Server-ID header", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		resp := httptest.NewRecorder().Result()
		resp.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		resp.Header.Set("X-Nonce", "test-nonce")
		resp.Header.Set("X-Signature", "test-signature")

		err := verifier.VerifyResponse(resp)

		if err == nil {
			t.Error("expected error for missing X-Server-ID header")
		}
	})

	t.Run("should reject response with missing X-Timestamp header", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		resp := httptest.NewRecorder().Result()
		resp.Header.Set("X-Server-ID", "server-123")
		resp.Header.Set("X-Nonce", "test-nonce")
		resp.Header.Set("X-Signature", "test-signature")

		err := verifier.VerifyResponse(resp)

		if err == nil {
			t.Error("expected error for missing X-Timestamp header")
		}
	})

	t.Run("should reject response with missing X-Nonce header", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		resp := httptest.NewRecorder().Result()
		resp.Header.Set("X-Server-ID", "server-123")
		resp.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		resp.Header.Set("X-Signature", "test-signature")

		err := verifier.VerifyResponse(resp)

		if err == nil {
			t.Error("expected error for missing X-Nonce header")
		}
	})

	t.Run("should reject response with missing X-Signature header", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		resp := httptest.NewRecorder().Result()
		resp.Header.Set("X-Server-ID", "server-123")
		resp.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		resp.Header.Set("X-Nonce", "test-nonce")

		err := verifier.VerifyResponse(resp)

		if err == nil {
			t.Error("expected error for missing X-Signature header")
		}
	})

	t.Run("should reject response with invalid signature", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		resp := httptest.NewRecorder().Result()
		resp.Header.Set("X-Server-ID", "server-123")
		resp.Header.Set("X-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
		resp.Header.Set("X-Nonce", "test-nonce")
		resp.Header.Set("X-Signature", "invalid-signature")

		err := verifier.VerifyResponse(resp)

		if err == nil {
			t.Error("expected error for invalid signature")
		}
	})

	t.Run("should reject response with expired timestamp", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		oldTimestamp := time.Now().Unix() - 400
		resp := createSignedResponse(t, privateKey, "server-123", oldTimestamp)

		err := verifier.VerifyResponse(resp)

		if err == nil {
			t.Error("expected error for expired timestamp")
		}
	})

	t.Run("should reject response with future timestamp", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		futureTimestamp := time.Now().Unix() + 400
		resp := createSignedResponse(t, privateKey, "server-123", futureTimestamp)

		err := verifier.VerifyResponse(resp)

		if err == nil {
			t.Error("expected error for future timestamp")
		}
	})

	t.Run("should accept response within timestamp window", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		timestamps := []int64{
			time.Now().Unix() - 250,
			time.Now().Unix(),
			time.Now().Unix() + 250,
		}

		for _, ts := range timestamps {
			resp := createSignedResponse(t, privateKey, "server-123", ts)
			err := verifier.VerifyResponse(resp)
			if err != nil {
				t.Errorf("expected no error for timestamp %d, got %v", ts, err)
			}
		}
	})
}

func TestRequestVerifier_ValidateTimestamp(t *testing.T) {
	t.Run("should accept current timestamp", func(t *testing.T) {
		publicKey := generateTestPublicKey(t)
		verifier := New(publicKey)

		timestamp := time.Now().Unix()

		err := verifier.validateTimestamp(timestamp)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("should accept timestamp within 5 minute window", func(t *testing.T) {
		publicKey := generateTestPublicKey(t)
		verifier := New(publicKey)

		timestamps := []int64{
			time.Now().Unix() - 299,
			time.Now().Unix() + 299,
		}

		for _, ts := range timestamps {
			err := verifier.validateTimestamp(ts)
			if err != nil {
				t.Errorf("expected no error for timestamp %d, got %v", ts, err)
			}
		}
	})

	t.Run("should reject timestamp older than 5 minutes", func(t *testing.T) {
		publicKey := generateTestPublicKey(t)
		verifier := New(publicKey)

		timestamp := time.Now().Unix() - 301

		err := verifier.validateTimestamp(timestamp)

		if err == nil {
			t.Error("expected error for old timestamp")
		}
	})

	t.Run("should reject timestamp more than 5 minutes in future", func(t *testing.T) {
		publicKey := generateTestPublicKey(t)
		verifier := New(publicKey)

		timestamp := time.Now().Unix() + 301

		err := verifier.validateTimestamp(timestamp)

		if err == nil {
			t.Error("expected error for future timestamp")
		}
	})
}

func TestRequestVerifier_VerifySignature(t *testing.T) {
	t.Run("should verify valid signature", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		serverID := "server-123"
		timestamp := "1234567890"
		nonce := "test-nonce"

		signature := generateSignature(t, privateKey, serverID, timestamp, nonce)

		err := verifier.verifySignature(serverID, timestamp, nonce, signature)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("should reject invalid signature", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		err := verifier.verifySignature("server-123", "1234567890", "test-nonce", "invalid-signature")

		if err == nil {
			t.Error("expected error for invalid signature")
		}
	})

	t.Run("should reject signature with tampered message", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		serverID := "server-123"
		timestamp := "1234567890"
		nonce := "test-nonce"

		signature := generateSignature(t, privateKey, serverID, timestamp, nonce)

		err := verifier.verifySignature("server-456", timestamp, nonce, signature)

		if err == nil {
			t.Error("expected error for tampered message")
		}
	})

	t.Run("should use correct message format SID|timestamp|nonce", func(t *testing.T) {
		privateKey := generateTestPrivateKey(t)
		verifier := New(&privateKey.PublicKey)

		serverID := "server-abc"
		timestamp := "9876543210"
		nonce := "unique-nonce"

		message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, nonce)
		hashed := sha256.Sum256([]byte(message))
		signatureBytes, _ := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hashed[:], nil)
		signature := base64.StdEncoding.EncodeToString(signatureBytes)

		err := verifier.verifySignature(serverID, timestamp, nonce, signature)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}

func generateTestPrivateKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test private key: %v", err)
	}

	return privateKey
}

func generateTestPublicKey(t *testing.T) *rsa.PublicKey {
	t.Helper()

	privateKey := generateTestPrivateKey(t)
	return &privateKey.PublicKey
}

func generateSignature(t *testing.T, privateKey *rsa.PrivateKey, serverID, timestamp, nonce string) string {
	t.Helper()

	message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, nonce)
	hashed := sha256.Sum256([]byte(message))

	signatureBytes, err := rsa.SignPSS(rand.Reader, privateKey, crypto.SHA256, hashed[:], nil)
	if err != nil {
		t.Fatalf("failed to generate signature: %v", err)
	}

	return base64.StdEncoding.EncodeToString(signatureBytes)
}

func createSignedResponse(t *testing.T, privateKey *rsa.PrivateKey, serverID string, timestamp int64) *http.Response {
	t.Helper()

	timestampStr := strconv.FormatInt(timestamp, 10)
	nonce := "test-nonce-" + timestampStr
	signature := generateSignature(t, privateKey, serverID, timestampStr, nonce)

	resp := httptest.NewRecorder().Result()
	resp.Header.Set("X-Server-ID", serverID)
	resp.Header.Set("X-Timestamp", timestampStr)
	resp.Header.Set("X-Nonce", nonce)
	resp.Header.Set("X-Signature", signature)

	return resp
}

func saveTestPublicKey(t *testing.T, dir string, publicKey *rsa.PublicKey) string {
	t.Helper()

	keyPath := filepath.Join(dir, "test-server-public.key")

	publicKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatalf("failed to marshal public key: %v", err)
	}

	publicKeyPEM := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}

	file, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("failed to create key file: %v", err)
	}
	defer file.Close()

	if err := pem.Encode(file, publicKeyPEM); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	return keyPath
}