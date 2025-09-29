package nonce

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestNonce_Creation(t *testing.T) {
	t.Run("should generate unique nonces on each call", func(t *testing.T) {
		nonces := make(map[string]bool)
		numNonces := 100

		for range numNonces {
			n := NewNonce()

			if n.Value == "" {
				t.Fatal("Nonce value should not be empty")
			}

			if nonces[n.Value] {
				t.Fatalf("Duplicate nonce found: %s", n.Value)
			}
			nonces[n.Value] = true
		}

		if len(nonces) != numNonces {
			t.Errorf("Expected %d unique nonces, got %d", numNonces, len(nonces))
		}
	})

	t.Run("should generate base64 encoded nonce with 8 bytes", func(t *testing.T) {
		n := NewNonce()

		// Decode the base64 string
		decoded, err := base64.URLEncoding.DecodeString(n.Value)
		if err != nil {
			t.Fatalf("Failed to decode base64 nonce: %v", err)
		}

		// Check that it's exactly 8 bytes
		if len(decoded) != 8 {
			t.Errorf("Expected 8 bytes, got %d bytes", len(decoded))
		}

		// Verify it's valid base64 by encoding back and comparing
		reencoded := base64.URLEncoding.EncodeToString(decoded)
		if reencoded != n.Value {
			t.Error("Nonce is not properly base64 encoded")
		}
	})

	t.Run("should auto-set CreatedAt timestamp", func(t *testing.T) {
		beforeCreation := time.Now()
		n := NewNonce()
		afterCreation := time.Now()

		if n.CreatedAt.IsZero() {
			t.Error("CreatedAt should not be zero")
		}

		if n.CreatedAt.Before(beforeCreation) {
			t.Error("CreatedAt should not be before nonce creation")
		}

		if n.CreatedAt.After(afterCreation) {
			t.Error("CreatedAt should not be after nonce creation")
		}
	})
}

func TestNonce_Validation(t *testing.T) {
	t.Run("should reject empty nonce", func(t *testing.T) {
		n := &Nonce{
			Value:     "",
			CreatedAt: time.Now(),
		}

		err := n.Validate()
		if err == nil {
			t.Error("Expected error for empty nonce, got nil")
		}

		if err != nil && err.Error() != "nonce value cannot be empty" {
			t.Errorf("Expected 'nonce value cannot be empty', got '%s'", err.Error())
		}
	})

	t.Run("should validate nonce format", func(t *testing.T) {
		testCases := []struct {
			name      string
			nonce     *Nonce
			wantError bool
			errMsg    string
		}{
			{
				name: "valid base64 nonce",
				nonce: &Nonce{
					Value:     base64.URLEncoding.EncodeToString([]byte("validnonce")),
					CreatedAt: time.Now(),
				},
				wantError: false,
			},
			{
				name: "invalid base64 characters",
				nonce: &Nonce{
					Value:     "not!valid@base64#",
					CreatedAt: time.Now(),
				},
				wantError: true,
				errMsg:    "nonce value is not valid base64",
			},
			{
				name: "malformed base64 padding",
				nonce: &Nonce{
					Value:     "invalid===",
					CreatedAt: time.Now(),
				},
				wantError: true,
				errMsg:    "nonce value is not valid base64",
			},
		}

		for _, tc := range testCases {
			err := tc.nonce.Validate()

			if tc.wantError && err == nil {
				t.Errorf("%s: expected error but got nil", tc.name)
			}

			if !tc.wantError && err != nil {
				t.Errorf("%s: expected no error but got %v", tc.name, err)
			}

			if tc.wantError && err != nil && err.Error() != tc.errMsg {
				t.Errorf("%s: expected error '%s' but got '%s'", tc.name, tc.errMsg, err.Error())
			}
		}
	})
}

func TestNonce_Expiry(t *testing.T) {
	t.Run("should identify expired nonces older than 5 minutes", func(t *testing.T) {
		// Create a nonce with CreatedAt older than 5 minutes
		oldTime := time.Now().Add(-6 * time.Minute)
		n := &Nonce{
			Value:     base64.URLEncoding.EncodeToString([]byte("testnonce")),
			CreatedAt: oldTime,
		}

		if !n.IsExpired() {
			t.Error("Expected nonce older than 5 minutes to be expired")
		}

		// Test edge case: exactly 5 minutes old
		exactlyFiveMinutes := time.Now().Add(-5 * time.Minute)
		n2 := &Nonce{
			Value:     base64.URLEncoding.EncodeToString([]byte("testnonce2")),
			CreatedAt: exactlyFiveMinutes,
		}

		if !n2.IsExpired() {
			t.Error("Expected nonce exactly 5 minutes old to be expired")
		}
	})

	t.Run("should identify valid nonces within 5 minute window", func(t *testing.T) {
		// Test nonce created just now
		n := &Nonce{
			Value:     base64.URLEncoding.EncodeToString([]byte("testnonce")),
			CreatedAt: time.Now(),
		}

		if n.IsExpired() {
			t.Error("Expected newly created nonce to not be expired")
		}

		// Test nonce created 2 minutes ago
		twoMinutesAgo := time.Now().Add(-2 * time.Minute)
		n2 := &Nonce{
			Value:     base64.URLEncoding.EncodeToString([]byte("testnonce2")),
			CreatedAt: twoMinutesAgo,
		}

		if n2.IsExpired() {
			t.Error("Expected nonce created 2 minutes ago to not be expired")
		}

		// Test nonce created 4 minutes 59 seconds ago
		almostFiveMinutes := time.Now().Add(-4*time.Minute - 59*time.Second)
		n3 := &Nonce{
			Value:     base64.URLEncoding.EncodeToString([]byte("testnonce3")),
			CreatedAt: almostFiveMinutes,
		}

		if n3.IsExpired() {
			t.Error("Expected nonce created 4:59 ago to not be expired")
		}
	})
}

