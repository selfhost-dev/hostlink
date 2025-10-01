package agentauth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"hostlink/domain/agent"

	"github.com/labstack/echo/v4"
)

func TestMiddleware(t *testing.T) {
	t.Run("allows request with valid signature", func(t *testing.T) {
		privateKey, publicKeyBase64 := generateTestKeys(t)
		repo := &mockAgentRepository{
			getPublicKeyByAgentID: func(ctx context.Context, agentID string) (string, error) {
				if agentID == "agt_test123" {
					return publicKeyBase64, nil
				}
				return "", agent.ErrAgentNotFound
			},
		}

		e := echo.New()
		req := createSignedHTTPRequest(testRequest{}, privateKey)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		middleware := Middleware(repo)
		handler := middleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "success")
		})

		err := handler(c)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status 200, got: %d", rec.Code)
		}
	})

	t.Run("returns 401 when X-Agent-ID header is missing", func(t *testing.T) {
		repo := &mockAgentRepository{}

		e := echo.New()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		middleware := Middleware(repo)
		handler := middleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "success")
		})

		err := handler(c)
		if err == nil {
			t.Error("Expected error, got nil")
		}

		if he, ok := err.(*echo.HTTPError); ok {
			if he.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401, got: %d", he.Code)
			}
		} else {
			t.Error("Expected echo.HTTPError")
		}
	})

	t.Run("returns 401 when agent not found in database", func(t *testing.T) {
		privateKey, _ := generateTestKeys(t)
		repo := &mockAgentRepository{
			getPublicKeyByAgentID: func(ctx context.Context, agentID string) (string, error) {
				return "", agent.ErrAgentNotFound
			},
		}

		e := echo.New()
		req := createSignedHTTPRequest(testRequest{}, privateKey)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		middleware := Middleware(repo)
		handler := middleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "success")
		})

		err := handler(c)
		if err == nil {
			t.Error("Expected error, got nil")
		}

		if he, ok := err.(*echo.HTTPError); ok {
			if he.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401, got: %d", he.Code)
			}
		} else {
			t.Error("Expected echo.HTTPError")
		}
	})

	t.Run("returns 401 when agent has no public key", func(t *testing.T) {
		privateKey, _ := generateTestKeys(t)
		repo := &mockAgentRepository{
			getPublicKeyByAgentID: func(ctx context.Context, agentID string) (string, error) {
				return "", agent.ErrPublicKeyNotFound
			},
		}

		e := echo.New()
		req := createSignedHTTPRequest(testRequest{}, privateKey)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		middleware := Middleware(repo)
		handler := middleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "success")
		})

		err := handler(c)
		if err == nil {
			t.Error("Expected error, got nil")
		}

		if he, ok := err.(*echo.HTTPError); ok {
			if he.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401, got: %d", he.Code)
			}
		} else {
			t.Error("Expected echo.HTTPError")
		}
	})

	t.Run("returns 401 when signature verification fails", func(t *testing.T) {
		privateKey, publicKeyBase64 := generateTestKeys(t)
		repo := &mockAgentRepository{
			getPublicKeyByAgentID: func(ctx context.Context, agentID string) (string, error) {
				return publicKeyBase64, nil
			},
		}

		e := echo.New()
		req := createSignedHTTPRequest(testRequest{signature: "invalid-signature-base64"}, privateKey)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		middleware := Middleware(repo)
		handler := middleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "success")
		})

		err := handler(c)
		if err == nil {
			t.Error("Expected error, got nil")
		}

		if he, ok := err.(*echo.HTTPError); ok {
			if he.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401, got: %d", he.Code)
			}
		} else {
			t.Error("Expected echo.HTTPError")
		}
	})

	t.Run("returns 401 when timestamp is outside valid window", func(t *testing.T) {
		privateKey, publicKeyBase64 := generateTestKeys(t)
		repo := &mockAgentRepository{
			getPublicKeyByAgentID: func(ctx context.Context, agentID string) (string, error) {
				return publicKeyBase64, nil
			},
		}

		e := echo.New()
		oldTimestamp := time.Now().Unix() - 400
		req := createSignedHTTPRequest(testRequest{timestamp: oldTimestamp}, privateKey)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		middleware := Middleware(repo)
		handler := middleware(func(c echo.Context) error {
			return c.String(http.StatusOK, "success")
		})

		err := handler(c)
		if err == nil {
			t.Error("Expected error, got nil")
		}

		if he, ok := err.(*echo.HTTPError); ok {
			if he.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401, got: %d", he.Code)
			}
		} else {
			t.Error("Expected echo.HTTPError")
		}
	})
}

type testRequest struct {
	agentID   string
	timestamp int64
	nonce     string
	signature string
}

type mockAgentRepository struct {
	getPublicKeyByAgentID func(ctx context.Context, agentID string) (string, error)
}

func (m *mockAgentRepository) GetPublicKeyByAgentID(ctx context.Context, agentID string) (string, error) {
	if m.getPublicKeyByAgentID != nil {
		return m.getPublicKeyByAgentID(ctx, agentID)
	}
	return "", nil
}

func generateTestKeys(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("Failed to marshal public key: %v", err)
	}

	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyDER)
	return privateKey, publicKeyBase64
}

func createSignedHTTPRequest(tr testRequest, privateKey *rsa.PrivateKey) *http.Request {
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
		panic(err)
	}

	if tr.signature == "" {
		tr.signature = base64.StdEncoding.EncodeToString(signature)
	}

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set("X-Agent-ID", tr.agentID)
	req.Header.Set("X-Timestamp", strconv.FormatInt(tr.timestamp, 10))
	req.Header.Set("X-Nonce", tr.nonce)
	req.Header.Set("X-Signature", tr.signature)

	return req
}
