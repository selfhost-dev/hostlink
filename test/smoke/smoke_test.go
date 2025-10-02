//go:build smoke

package smoke

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApplicationSmoke(t *testing.T) {
	baseURL := "http://localhost:8080"

	t.Run("health endpoint should respond", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/health")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("registration endpoint should accept valid requests", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"fingerprint":     fmt.Sprintf("smoke-test-%d", time.Now().Unix()),
			"token_id":        "test-token-id",
			"token_key":       "test-token-key",
			"public_key":      "ssh-rsa AAAAB3SmokeTest...",
			"public_key_type": "rsa",
		}

		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		resp, err := http.Post(baseURL+"/api/v1/agents/register", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode, "Registration should succeed with valid request")
	})

	t.Run("registration endpoint should reject invalid requests", func(t *testing.T) {
		reqBody := map[string]interface{}{
			"fingerprint": "missing-required-fields",
		}

		body, err := json.Marshal(reqBody)
		require.NoError(t, err)

		resp, err := http.Post(baseURL+"/api/v1/agents/register", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "Should reject request with missing required fields")
	})
}
