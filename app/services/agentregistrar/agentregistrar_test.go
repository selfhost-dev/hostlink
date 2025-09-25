package agentregistrar

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	t.Run("should create registrar with HTTP client", func(t *testing.T) {
		registrar := New()

		if registrar == nil {
			t.Fatal("New() returned nil")
		}

		if registrar.client == nil {
			t.Fatal("registrar.client is nil")
		}

		if registrar.client.Timeout != 30*time.Second {
			t.Errorf("Expected timeout of 30s, got %v", registrar.client.Timeout)
		}
	})

	t.Run("should set 30 second timeout", func(t *testing.T) {
		registrar := New()

		expected := 30 * time.Second
		actual := registrar.client.Timeout

		if actual != expected {
			t.Errorf("Expected timeout %v, got %v", expected, actual)
		}
	})

	t.Run("should load control plane URL from config", func(t *testing.T) {
		expectedURL := "https://test.control-plane.com"
		cfg := &Config{
			ControlPlaneURL: expectedURL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		if registrar.controlPlaneURL != expectedURL {
			t.Errorf("Expected control plane URL %s, got %s", expectedURL, registrar.controlPlaneURL)
		}
	})

	t.Run("should load token credentials from config", func(t *testing.T) {
		expectedTokenID := "test-token-id-123"
		expectedTokenKey := "test-token-key-secret"
		cfg := &Config{
			ControlPlaneURL: "https://test.example.com",
			TokenID:         expectedTokenID,
			TokenKey:        expectedTokenKey,
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		if registrar.tokenID != expectedTokenID {
			t.Errorf("Expected token ID %s, got %s", expectedTokenID, registrar.tokenID)
		}

		if registrar.tokenKey != expectedTokenKey {
			t.Errorf("Expected token key %s, got %s", expectedTokenKey, registrar.tokenKey)
		}
	})
}

func TestRegister(t *testing.T) {
	t.Run("should send correct registration request", func(t *testing.T) {
		var capturedRequest RegistrationRequest

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify HTTP method and path
			if r.Method != "POST" {
				t.Errorf("Expected POST method, got %s", r.Method)
			}

			if r.URL.Path != "/agent/v1/register" {
				t.Errorf("Expected path /agent/v1/register, got %s", r.URL.Path)
			}

			// Verify Content-Type header
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("Expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
			}

			// Capture request body
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("Failed to read request body: %v", err)
			}

			if err := json.Unmarshal(body, &capturedRequest); err != nil {
				t.Fatalf("Failed to unmarshal request: %v", err)
			}

			// Send success response
			response := RegistrationResponse{
				AgentID:      "agt_test123",
				Fingerprint:  capturedRequest.Fingerprint,
				Status:       "registered",
				Message:      "Success",
				RegisteredAt: time.Now(),
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-token-id",
			TokenKey:        "test-token-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		fingerprint := "test-fingerprint-123"
		publicKey := "test-public-key-base64"
		tags := []TagPair{
			{Key: "env", Value: "test"},
			{Key: "version", Value: "1.0.0"},
		}

		resp, err := registrar.Register(fingerprint, publicKey, tags)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		// Verify the captured request
		if capturedRequest.Fingerprint != fingerprint {
			t.Errorf("Expected fingerprint %s, got %s", fingerprint, capturedRequest.Fingerprint)
		}

		if capturedRequest.TokenID != cfg.TokenID {
			t.Errorf("Expected token ID %s, got %s", cfg.TokenID, capturedRequest.TokenID)
		}

		if capturedRequest.TokenKey != cfg.TokenKey {
			t.Errorf("Expected token key %s, got %s", cfg.TokenKey, capturedRequest.TokenKey)
		}

		if capturedRequest.PublicKey != publicKey {
			t.Errorf("Expected public key %s, got %s", publicKey, capturedRequest.PublicKey)
		}

		if capturedRequest.PublicKeyType != "RSA" {
			t.Errorf("Expected public key type RSA, got %s", capturedRequest.PublicKeyType)
		}

		if len(capturedRequest.Tags) != len(tags) {
			t.Errorf("Expected %d tags, got %d", len(tags), len(capturedRequest.Tags))
		}

		// Verify response
		if resp.AgentID != "agt_test123" {
			t.Errorf("Expected agent ID agt_test123, got %s", resp.AgentID)
		}
	})

	t.Run("should include all required fields in request", func(t *testing.T) {
		requiredFields := map[string]bool{
			"fingerprint":     false,
			"token_id":        false,
			"token_key":       false,
			"public_key":      false,
			"public_key_type": false,
			"tags":            false,
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("Failed to read request body: %v", err)
			}

			var requestMap map[string]interface{}
			if err := json.Unmarshal(body, &requestMap); err != nil {
				t.Fatalf("Failed to unmarshal request: %v", err)
			}

			// Check all required fields are present
			for field := range requiredFields {
				if _, exists := requestMap[field]; exists {
					requiredFields[field] = true
				}
			}

			// Send success response
			response := RegistrationResponse{
				AgentID:      "agt_test",
				Fingerprint:  "test",
				Status:       "registered",
				Message:      "Success",
				RegisteredAt: time.Now(),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		_, err := registrar.Register("fingerprint", "public-key", []TagPair{{Key: "test", Value: "value"}})
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		// Verify all required fields were sent
		for field, wasPresent := range requiredFields {
			if !wasPresent {
				t.Errorf("Required field '%s' was not included in request", field)
			}
		}
	})

	t.Run("should return success response on 200 OK", func(t *testing.T) {
		expectedResponse := RegistrationResponse{
			AgentID:      "agt_success_123",
			Fingerprint:  "fp_test_456",
			Status:       "registered",
			Message:      "Agent successfully registered",
			RegisteredAt: time.Now(),
		}

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(expectedResponse)
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-token-id",
			TokenKey:        "test-token-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		resp, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err != nil {
			t.Fatalf("Expected successful registration, got error: %v", err)
		}

		if resp == nil {
			t.Fatal("Expected response, got nil")
		}

		if resp.AgentID != expectedResponse.AgentID {
			t.Errorf("Expected AgentID %s, got %s", expectedResponse.AgentID, resp.AgentID)
		}

		if resp.Fingerprint != expectedResponse.Fingerprint {
			t.Errorf("Expected Fingerprint %s, got %s", expectedResponse.Fingerprint, resp.Fingerprint)
		}

		if resp.Status != expectedResponse.Status {
			t.Errorf("Expected Status %s, got %s", expectedResponse.Status, resp.Status)
		}

		if resp.Message != expectedResponse.Message {
			t.Errorf("Expected Message %s, got %s", expectedResponse.Message, resp.Message)
		}
	})

	t.Run("should return error when tokens not configured", func(t *testing.T) {
		// Test with empty TokenID
		cfg := &Config{
			ControlPlaneURL: "https://test.example.com",
			TokenID:         "",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		resp, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err == nil {
			t.Fatal("Expected error when TokenID is empty, got nil")
		}

		if resp != nil {
			t.Error("Expected nil response when error occurs")
		}

		expectedError := "token credentials not configured"
		if err.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
		}

		// Test with empty TokenKey
		cfg2 := &Config{
			ControlPlaneURL: "https://test.example.com",
			TokenID:         "test-id",
			TokenKey:        "",
			Timeout:         30 * time.Second,
		}

		registrar2 := NewWithConfig(cfg2)

		resp2, err2 := registrar2.Register("test-fp", "test-key", []TagPair{})
		if err2 == nil {
			t.Fatal("Expected error when TokenKey is empty, got nil")
		}

		if resp2 != nil {
			t.Error("Expected nil response when error occurs")
		}

		if err2.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err2.Error())
		}

		// Test with both empty
		cfg3 := &Config{
			ControlPlaneURL: "https://test.example.com",
			TokenID:         "",
			TokenKey:        "",
			Timeout:         30 * time.Second,
		}

		registrar3 := NewWithConfig(cfg3)

		resp3, err3 := registrar3.Register("test-fp", "test-key", []TagPair{})
		if err3 == nil {
			t.Fatal("Expected error when both tokens are empty, got nil")
		}

		if resp3 != nil {
			t.Error("Expected nil response when error occurs")
		}

		if err3.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err3.Error())
		}
	})

	t.Run("should return error on network failure", func(t *testing.T) {
		// Use an invalid URL that will cause a network failure
		cfg := &Config{
			ControlPlaneURL: "http://localhost:99999", // Invalid port
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         1 * time.Second, // Short timeout to speed up test
		}

		registrar := NewWithConfig(cfg)

		resp, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err == nil {
			t.Fatal("Expected error on network failure, got nil")
		}

		if resp != nil {
			t.Error("Expected nil response when network error occurs")
		}

		// Check that error message indicates registration request failed
		if !strings.Contains(err.Error(), "registration request failed") {
			t.Errorf("Expected error to contain 'registration request failed', got: %s", err.Error())
		}
	})

	t.Run("should return error on 400 Bad Request", func(t *testing.T) {
		errorMessage := "Invalid registration request format"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"error": errorMessage,
			})
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		resp, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err == nil {
			t.Fatal("Expected error on 400 Bad Request, got nil")
		}

		if resp != nil {
			t.Error("Expected nil response when error occurs")
		}

		expectedError := "registration failed: " + errorMessage
		if err.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("should return error on 401 Unauthorized", func(t *testing.T) {
		errorMessage := "Invalid token credentials"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"error": errorMessage,
			})
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "invalid-id",
			TokenKey:        "invalid-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		resp, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err == nil {
			t.Fatal("Expected error on 401 Unauthorized, got nil")
		}

		if resp != nil {
			t.Error("Expected nil response when error occurs")
		}

		expectedError := "registration failed: " + errorMessage
		if err.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("should return error on 500 Internal Server Error", func(t *testing.T) {
		errorMessage := "Internal server error occurred"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"error": errorMessage,
			})
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		resp, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err == nil {
			t.Fatal("Expected error on 500 Internal Server Error, got nil")
		}

		if resp != nil {
			t.Error("Expected nil response when error occurs")
		}

		expectedError := "registration failed: " + errorMessage
		if err.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("should parse error response from server", func(t *testing.T) {
		testCases := []struct {
			name          string
			statusCode    int
			errorMessage  string
			expectedError string
		}{
			{
				name:          "with error field",
				statusCode:    http.StatusBadRequest,
				errorMessage:  "Custom error message from server",
				expectedError: "registration failed: Custom error message from server",
			},
			{
				name:          "forbidden with message",
				statusCode:    http.StatusForbidden,
				errorMessage:  "Access denied",
				expectedError: "registration failed: Access denied",
			},
			{
				name:          "conflict with message",
				statusCode:    http.StatusConflict,
				errorMessage:  "Agent already registered",
				expectedError: "registration failed: Agent already registered",
			},
		}

		for _, tc := range testCases {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]string{
					"error": tc.errorMessage,
				})
			}))

			cfg := &Config{
				ControlPlaneURL: server.URL,
				TokenID:         "test-id",
				TokenKey:        "test-key",
				Timeout:         30 * time.Second,
			}

			registrar := NewWithConfig(cfg)

			resp, err := registrar.Register("test-fp", "test-key", []TagPair{})
			if err == nil {
				t.Errorf("[%s] Expected error, got nil", tc.name)
			}

			if resp != nil {
				t.Errorf("[%s] Expected nil response when error occurs", tc.name)
			}

			if err != nil && err.Error() != tc.expectedError {
				t.Errorf("[%s] Expected error message '%s', got '%s'", tc.name, tc.expectedError, err.Error())
			}

			server.Close()
		}

		// Test case when server returns non-JSON error response
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("Plain text error"))
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		resp, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err == nil {
			t.Fatal("Expected error for non-JSON response, got nil")
		}

		if resp != nil {
			t.Error("Expected nil response when error occurs")
		}

		expectedError := "registration failed with status 400"
		if err.Error() != expectedError {
			t.Errorf("Expected error message '%s', got '%s'", expectedError, err.Error())
		}
	})

	t.Run("should handle invalid JSON response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.Header().Set("Content-Type", "application/json")
			// Send invalid JSON
			w.Write([]byte(`{"agent_id": "test", "status": "registered", invalid json here}`))
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		resp, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err == nil {
			t.Fatal("Expected error for invalid JSON response, got nil")
		}

		if resp != nil {
			t.Error("Expected nil response when JSON parsing fails")
		}

		if !strings.Contains(err.Error(), "failed to parse response") {
			t.Errorf("Expected error to contain 'failed to parse response', got: %s", err.Error())
		}
	})

	t.Run("should set correct Content-Type header", func(t *testing.T) {
		var capturedContentType string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedContentType = r.Header.Get("Content-Type")

			response := RegistrationResponse{
				AgentID:      "agt_test",
				Fingerprint:  "test",
				Status:       "registered",
				Message:      "Success",
				RegisteredAt: time.Now(),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		_, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		expectedContentType := "application/json"
		if capturedContentType != expectedContentType {
			t.Errorf("Expected Content-Type header '%s', got '%s'", expectedContentType, capturedContentType)
		}
	})

	t.Run("should use correct registration endpoint URL", func(t *testing.T) {
		var capturedPath string

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedPath = r.URL.Path

			response := RegistrationResponse{
				AgentID:      "agt_test",
				Fingerprint:  "test",
				Status:       "registered",
				Message:      "Success",
				RegisteredAt: time.Now(),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		_, err := registrar.Register("test-fp", "test-key", []TagPair{})
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		expectedPath := "/agent/v1/register"
		if capturedPath != expectedPath {
			t.Errorf("Expected endpoint path '%s', got '%s'", expectedPath, capturedPath)
		}
	})

	t.Run("should include tags in registration request", func(t *testing.T) {
		var capturedRequest RegistrationRequest

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("Failed to read request body: %v", err)
			}

			if err := json.Unmarshal(body, &capturedRequest); err != nil {
				t.Fatalf("Failed to unmarshal request: %v", err)
			}

			response := RegistrationResponse{
				AgentID:      "agt_test",
				Fingerprint:  "test",
				Status:       "registered",
				Message:      "Success",
				RegisteredAt: time.Now(),
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
		}))
		defer server.Close()

		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		tags := []TagPair{
			{Key: "environment", Value: "production"},
			{Key: "region", Value: "us-west-2"},
			{Key: "version", Value: "1.2.3"},
		}

		_, err := registrar.Register("test-fp", "test-key", tags)
		if err != nil {
			t.Fatalf("Register failed: %v", err)
		}

		// Verify tags were included in the request
		if len(capturedRequest.Tags) != len(tags) {
			t.Errorf("Expected %d tags, got %d", len(tags), len(capturedRequest.Tags))
		}

		// Verify each tag
		for i, expectedTag := range tags {
			if i >= len(capturedRequest.Tags) {
				break
			}
			actualTag := capturedRequest.Tags[i]
			if actualTag.Key != expectedTag.Key {
				t.Errorf("Tag[%d] Key: expected '%s', got '%s'", i, expectedTag.Key, actualTag.Key)
			}
			if actualTag.Value != expectedTag.Value {
				t.Errorf("Tag[%d] Value: expected '%s', got '%s'", i, expectedTag.Value, actualTag.Value)
			}
		}

		// Test with empty tags
		capturedRequest = RegistrationRequest{}
		_, err = registrar.Register("test-fp", "test-key", []TagPair{})
		if err != nil {
			t.Fatalf("Register with empty tags failed: %v", err)
		}

		if capturedRequest.Tags == nil {
			t.Log("Tags field is nil for empty tags (expected behavior)")
		} else if len(capturedRequest.Tags) != 0 {
			t.Errorf("Expected empty tags array, got %d tags", len(capturedRequest.Tags))
		}
	})
}

func TestPreparePublicKey(t *testing.T) {
	t.Run("should generate new keypair if not exists", func(t *testing.T) {
		tempDir := t.TempDir()
		keyPath := tempDir + "/test_agent.key"

		cfg := &Config{
			ControlPlaneURL: "https://test.example.com",
			TokenID:         "test-id",
			TokenKey:        "test-key",
			PrivateKeyPath:  keyPath,
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		// Verify key doesn't exist yet
		if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
			t.Fatal("Key file should not exist before PreparePublicKey")
		}

		// Call PreparePublicKey - should generate new keypair
		publicKey, err := registrar.PreparePublicKey()
		if err != nil {
			t.Fatalf("PreparePublicKey failed: %v", err)
		}

		if publicKey == "" {
			t.Error("Expected non-empty public key")
		}

		// Verify key file was created
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			t.Error("Key file should exist after PreparePublicKey")
		}

		// Verify the public key is valid Base64
		if _, err := base64.StdEncoding.DecodeString(publicKey); err != nil {
			t.Errorf("Public key is not valid Base64: %v", err)
		}
	})

	t.Run("should load existing keypair if exists", func(t *testing.T) {
		tempDir := t.TempDir()
		keyPath := tempDir + "/test_agent.key"

		cfg := &Config{
			ControlPlaneURL: "https://test.example.com",
			TokenID:         "test-id",
			TokenKey:        "test-key",
			PrivateKeyPath:  keyPath,
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		// First call - should generate new keypair
		publicKey1, err := registrar.PreparePublicKey()
		if err != nil {
			t.Fatalf("First PreparePublicKey failed: %v", err)
		}

		// Verify key file was created
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			t.Fatal("Key file should exist after first PreparePublicKey")
		}

		// Second call - should load existing keypair
		publicKey2, err := registrar.PreparePublicKey()
		if err != nil {
			t.Fatalf("Second PreparePublicKey failed: %v", err)
		}

		// Both calls should return the same public key
		if publicKey1 != publicKey2 {
			t.Error("Expected same public key from existing keypair")
		}

		// Create a new registrar instance to verify it loads the same key
		registrar2 := NewWithConfig(cfg)
		publicKey3, err := registrar2.PreparePublicKey()
		if err != nil {
			t.Fatalf("PreparePublicKey with new registrar failed: %v", err)
		}

		if publicKey1 != publicKey3 {
			t.Error("New registrar instance should load the same existing keypair")
		}
	})

	t.Run("should return Base64 encoded public key", func(t *testing.T) {
		tempDir := t.TempDir()
		keyPath := tempDir + "/test_agent.key"

		cfg := &Config{
			ControlPlaneURL: "https://test.example.com",
			TokenID:         "test-id",
			TokenKey:        "test-key",
			PrivateKeyPath:  keyPath,
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		publicKey, err := registrar.PreparePublicKey()
		if err != nil {
			t.Fatalf("PreparePublicKey failed: %v", err)
		}

		// Verify it's not empty
		if publicKey == "" {
			t.Fatal("Public key should not be empty")
		}

		// Verify it's valid Base64
		decodedBytes, err := base64.StdEncoding.DecodeString(publicKey)
		if err != nil {
			t.Errorf("Public key is not valid Base64: %v", err)
		}

		// Verify decoded bytes are not empty
		if len(decodedBytes) == 0 {
			t.Error("Decoded public key should not be empty")
		}

		// Verify the Base64 string only contains valid Base64 characters
		validBase64Chars := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
		for _, char := range publicKey {
			if !strings.Contains(validBase64Chars, string(char)) {
				t.Errorf("Public key contains invalid Base64 character: %c", char)
			}
		}
	})

	t.Run("should return error if keypair generation fails", func(t *testing.T) {
		// Use an invalid path that will cause an error
		invalidPath := "/invalid\x00path/with/null/byte.key"

		cfg := &Config{
			ControlPlaneURL: "https://test.example.com",
			TokenID:         "test-id",
			TokenKey:        "test-key",
			PrivateKeyPath:  invalidPath,
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		publicKey, err := registrar.PreparePublicKey()
		if err == nil {
			t.Fatal("Expected error for invalid key path, got nil")
		}

		if publicKey != "" {
			t.Error("Expected empty public key when error occurs")
		}

		if !strings.Contains(err.Error(), "failed to load/generate keypair") {
			t.Errorf("Expected error to contain 'failed to load/generate keypair', got: %s", err.Error())
		}
	})

	t.Run("should use configured private key path", func(t *testing.T) {
		tempDir := t.TempDir()
		customPath := tempDir + "/custom/path/to/agent.key"

		cfg := &Config{
			ControlPlaneURL: "https://test.example.com",
			TokenID:         "test-id",
			TokenKey:        "test-key",
			PrivateKeyPath:  customPath,
			Timeout:         30 * time.Second,
		}

		registrar := NewWithConfig(cfg)

		// Call PreparePublicKey which should create key at custom path
		_, err := registrar.PreparePublicKey()
		if err != nil {
			t.Fatalf("PreparePublicKey failed: %v", err)
		}

		// Verify the key was created at the configured path
		if _, err := os.Stat(customPath); os.IsNotExist(err) {
			t.Errorf("Key file should exist at configured path: %s", customPath)
		}

		// Verify the parent directories were created
		parentDir := tempDir + "/custom/path/to"
		if info, err := os.Stat(parentDir); os.IsNotExist(err) {
			t.Error("Parent directory should have been created")
		} else if !info.IsDir() {
			t.Error("Parent path should be a directory")
		}

		// Verify no key was created at a default location
		defaultPath := tempDir + "/agent.key"
		if _, err := os.Stat(defaultPath); !os.IsNotExist(err) {
			t.Error("Key should not be created at default path when custom path is configured")
		}
	})
}

func TestGetDefaultTags(t *testing.T) {
	t.Run("should include hostname tag", func(t *testing.T) {
		registrar := New()

		tags := registrar.GetDefaultTags()

		// Find the hostname tag
		var hostnameTag *TagPair
		for _, tag := range tags {
			if tag.Key == "hostname" {
				hostnameTag = &tag
				break
			}
		}

		if hostnameTag == nil {
			t.Fatal("Expected to find 'hostname' tag in default tags")
		}

		// Verify the hostname value is not empty
		if hostnameTag.Value == "" {
			t.Error("Hostname tag value should not be empty")
		}

		// Verify the hostname matches the system hostname
		expectedHostname, err := os.Hostname()
		if err != nil {
			t.Logf("Could not get system hostname for comparison: %v", err)
		} else {
			if hostnameTag.Value != expectedHostname {
				t.Errorf("Expected hostname '%s', got '%s'", expectedHostname, hostnameTag.Value)
			}
		}
	})

	t.Run("should include os tag as linux", func(t *testing.T) {
		registrar := New()

		tags := registrar.GetDefaultTags()

		// Find the os tag
		var osTag *TagPair
		for _, tag := range tags {
			if tag.Key == "os" {
				osTag = &tag
				break
			}
		}

		if osTag == nil {
			t.Fatal("Expected to find 'os' tag in default tags")
		}

		// Verify the os value is "linux"
		expectedOS := "linux"
		if osTag.Value != expectedOS {
			t.Errorf("Expected os tag value '%s', got '%s'", expectedOS, osTag.Value)
		}
	})

	t.Run("should return non-empty hostname", func(t *testing.T) {
		registrar := New()

		tags := registrar.GetDefaultTags()

		// Verify we have at least some tags
		if len(tags) == 0 {
			t.Fatal("Expected at least one tag in default tags")
		}

		// Find the hostname tag and verify it's not empty
		hostnameFound := false
		for _, tag := range tags {
			if tag.Key == "hostname" {
				hostnameFound = true
				if tag.Value == "" {
					t.Error("Hostname tag value should not be empty")
				}
				// Verify it's a reasonable hostname (not just whitespace)
				if strings.TrimSpace(tag.Value) == "" {
					t.Error("Hostname tag value should not be just whitespace")
				}
				if len(tag.Value) == 0 {
					t.Error("Hostname tag value should have non-zero length")
				}
				break
			}
		}

		if !hostnameFound {
			t.Error("Hostname tag was not found in default tags")
		}
	})
}

func TestRegistrationRequestSerialization(t *testing.T) {
	t.Run("should marshal request to valid JSON", func(t *testing.T) {
		request := RegistrationRequest{
			Fingerprint:   "test-fingerprint-123",
			TokenID:       "token-id-456",
			TokenKey:      "token-key-789",
			PublicKey:     "base64-encoded-public-key",
			PublicKeyType: "RSA",
			Tags: []TagPair{
				{Key: "env", Value: "production"},
				{Key: "version", Value: "1.0.0"},
			},
		}

		jsonData, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("Failed to marshal registration request: %v", err)
		}

		// Verify it's not empty
		if len(jsonData) == 0 {
			t.Error("Marshaled JSON should not be empty")
		}

		// Verify it's valid JSON by unmarshaling it back
		var unmarshaledRequest RegistrationRequest
		err = json.Unmarshal(jsonData, &unmarshaledRequest)
		if err != nil {
			t.Fatalf("Failed to unmarshal JSON: %v", err)
		}

		// Verify all fields are preserved
		if unmarshaledRequest.Fingerprint != request.Fingerprint {
			t.Errorf("Fingerprint not preserved: expected %s, got %s", request.Fingerprint, unmarshaledRequest.Fingerprint)
		}

		if unmarshaledRequest.TokenID != request.TokenID {
			t.Errorf("TokenID not preserved: expected %s, got %s", request.TokenID, unmarshaledRequest.TokenID)
		}

		if unmarshaledRequest.TokenKey != request.TokenKey {
			t.Errorf("TokenKey not preserved: expected %s, got %s", request.TokenKey, unmarshaledRequest.TokenKey)
		}

		if unmarshaledRequest.PublicKey != request.PublicKey {
			t.Errorf("PublicKey not preserved: expected %s, got %s", request.PublicKey, unmarshaledRequest.PublicKey)
		}

		if unmarshaledRequest.PublicKeyType != request.PublicKeyType {
			t.Errorf("PublicKeyType not preserved: expected %s, got %s", request.PublicKeyType, unmarshaledRequest.PublicKeyType)
		}

		if len(unmarshaledRequest.Tags) != len(request.Tags) {
			t.Errorf("Tags count not preserved: expected %d, got %d", len(request.Tags), len(unmarshaledRequest.Tags))
		}

		// Verify the JSON has the correct field names (snake_case)
		var jsonMap map[string]interface{}
		json.Unmarshal(jsonData, &jsonMap)

		expectedFields := []string{"fingerprint", "token_id", "token_key", "public_key", "public_key_type", "tags"}
		for _, field := range expectedFields {
			if _, exists := jsonMap[field]; !exists {
				t.Errorf("Expected JSON field '%s' not found", field)
			}
		}
	})

	t.Run("should include public_key_type as RSA", func(t *testing.T) {
		request := RegistrationRequest{
			Fingerprint:   "test-fp",
			TokenID:       "test-id",
			TokenKey:      "test-key",
			PublicKey:     "test-public-key",
			PublicKeyType: "RSA",
			Tags:          []TagPair{},
		}

		jsonData, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("Failed to marshal request: %v", err)
		}

		// Parse JSON to verify public_key_type field
		var jsonMap map[string]interface{}
		err = json.Unmarshal(jsonData, &jsonMap)
		if err != nil {
			t.Fatalf("Failed to unmarshal JSON: %v", err)
		}

		// Check that public_key_type field exists
		publicKeyType, exists := jsonMap["public_key_type"]
		if !exists {
			t.Fatal("public_key_type field not found in JSON")
		}

		// Verify the value is "RSA"
		if publicKeyType != "RSA" {
			t.Errorf("Expected public_key_type to be 'RSA', got '%v'", publicKeyType)
		}

		// Also verify when setting PublicKeyType explicitly
		request2 := RegistrationRequest{
			Fingerprint:   "test-fp",
			TokenID:       "test-id",
			TokenKey:      "test-key",
			PublicKey:     "test-public-key",
			PublicKeyType: "RSA",
			Tags:          nil,
		}

		jsonData2, err := json.Marshal(request2)
		if err != nil {
			t.Fatalf("Failed to marshal request2: %v", err)
		}

		// Verify it contains "RSA" in the JSON string
		jsonString := string(jsonData2)
		if !strings.Contains(jsonString, `"public_key_type":"RSA"`) {
			t.Errorf("JSON should contain public_key_type:RSA, got: %s", jsonString)
		}
	})
}

func TestRegistrationResponseDeserialization(t *testing.T) {
	t.Run("should parse valid registration response", func(t *testing.T) {
		now := time.Now().UTC()
		jsonResponse := fmt.Sprintf(`{
			"agent_id": "agt_123456",
			"fingerprint": "fp_abcdef",
			"status": "registered",
			"message": "Successfully registered",
			"registered_at": "%s"
		}`, now.Format(time.RFC3339Nano))

		var response RegistrationResponse
		err := json.Unmarshal([]byte(jsonResponse), &response)
		if err != nil {
			t.Fatalf("Failed to unmarshal registration response: %v", err)
		}

		// Verify all fields were parsed correctly
		if response.AgentID != "agt_123456" {
			t.Errorf("Expected AgentID 'agt_123456', got '%s'", response.AgentID)
		}

		if response.Fingerprint != "fp_abcdef" {
			t.Errorf("Expected Fingerprint 'fp_abcdef', got '%s'", response.Fingerprint)
		}

		if response.Status != "registered" {
			t.Errorf("Expected Status 'registered', got '%s'", response.Status)
		}

		if response.Message != "Successfully registered" {
			t.Errorf("Expected Message 'Successfully registered', got '%s'", response.Message)
		}

		// Check registered_at timestamp is close to the expected time
		timeDiff := response.RegisteredAt.Sub(now).Abs()
		if timeDiff > time.Second {
			t.Errorf("RegisteredAt time differs by %v, expected close to %v, got %v",
				timeDiff, now, response.RegisteredAt)
		}
	})

	t.Run("should parse registered_at timestamp", func(t *testing.T) {
		testCases := []struct {
			name      string
			timestamp string
			valid     bool
		}{
			{
				name:      "RFC3339 format",
				timestamp: "2024-01-15T10:30:45Z",
				valid:     true,
			},
			{
				name:      "RFC3339 with nanoseconds",
				timestamp: "2024-01-15T10:30:45.123456789Z",
				valid:     true,
			},
			{
				name:      "RFC3339 with timezone offset",
				timestamp: "2024-01-15T10:30:45+05:30",
				valid:     true,
			},
			{
				name:      "RFC3339 with negative timezone",
				timestamp: "2024-01-15T10:30:45-08:00",
				valid:     true,
			},
		}

		for _, tc := range testCases {
			jsonResponse := fmt.Sprintf(`{
				"agent_id": "agt_test",
				"fingerprint": "fp_test",
				"status": "registered",
				"message": "Test",
				"registered_at": "%s"
			}`, tc.timestamp)

			var response RegistrationResponse
			err := json.Unmarshal([]byte(jsonResponse), &response)

			if tc.valid {
				if err != nil {
					t.Errorf("[%s] Expected successful parsing, got error: %v", tc.name, err)
				}

				// Verify the timestamp was actually parsed
				if response.RegisteredAt.IsZero() {
					t.Errorf("[%s] RegisteredAt should not be zero time", tc.name)
				}

				// Verify we can format it back
				formatted := response.RegisteredAt.Format(time.RFC3339)
				if formatted == "" {
					t.Errorf("[%s] Should be able to format RegisteredAt back to string", tc.name)
				}
			} else {
				if err == nil {
					t.Errorf("[%s] Expected parsing error for invalid timestamp, got nil", tc.name)
				}
			}
		}

		// Test with specific known timestamp
		knownTime := time.Date(2024, 3, 15, 14, 30, 0, 0, time.UTC)
		jsonResponse := fmt.Sprintf(`{
			"agent_id": "agt_test",
			"fingerprint": "fp_test",
			"status": "registered",
			"message": "Test",
			"registered_at": "%s"
		}`, knownTime.Format(time.RFC3339))

		var response RegistrationResponse
		err := json.Unmarshal([]byte(jsonResponse), &response)
		if err != nil {
			t.Fatalf("Failed to unmarshal response with known timestamp: %v", err)
		}

		if !response.RegisteredAt.Equal(knownTime) {
			t.Errorf("Expected RegisteredAt to be %v, got %v", knownTime, response.RegisteredAt)
		}
	})
}

func TestHTTPTimeout(t *testing.T) {
	t.Run("should timeout after 30 seconds", func(t *testing.T) {
		// Create a server that delays response longer than timeout
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Delay for 2 seconds (simulating slow response)
			// We use 2 seconds instead of 31 to make the test faster
			time.Sleep(2 * time.Second)
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(RegistrationResponse{
				AgentID: "agt_delayed",
			})
		}))
		defer server.Close()

		// Create registrar with 1 second timeout for faster test
		cfg := &Config{
			ControlPlaneURL: server.URL,
			TokenID:         "test-id",
			TokenKey:        "test-key",
			Timeout:         1 * time.Second, // Short timeout for test
		}

		registrar := NewWithConfig(cfg)

		start := time.Now()
		_, err := registrar.Register("test-fp", "test-key", []TagPair{})
		elapsed := time.Since(start)

		// Should get a timeout error
		if err == nil {
			t.Fatal("Expected timeout error, got nil")
		}

		// Verify it's a timeout error
		if !strings.Contains(err.Error(), "timeout") && !strings.Contains(err.Error(), "deadline") {
			t.Errorf("Expected timeout/deadline error, got: %v", err)
		}

		// Verify timeout happened around the configured time (1 second)
		// Allow some margin for processing
		if elapsed < 900*time.Millisecond || elapsed > 2*time.Second {
			t.Errorf("Expected timeout around 1 second, actual time: %v", elapsed)
		}

		// Test that default timeout is 30 seconds when created with New()
		registrar2 := New()
		if registrar2.client.Timeout != 30*time.Second {
			t.Errorf("Expected default timeout of 30s, got %v", registrar2.client.Timeout)
		}
	})
}

