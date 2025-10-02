//go:build smoke

package smoke

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentDetailsSmoke(t *testing.T) {
	serverURL := os.Getenv("HOSTLINK_SERVER_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8080"
	}

	t.Run("creates agent then fetches by ID", func(t *testing.T) {
		registerPayload := map[string]interface{}{
			"fingerprint":     "smoke-test-fp-001",
			"token_id":        "smoke-token-id",
			"token_key":       "smoke-token-key",
			"public_key":      "smoke-public-key",
			"public_key_type": "RSA",
		}

		body, _ := json.Marshal(registerPayload)
		resp, err := http.Post(serverURL+"/api/v1/agents/register", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()

		var regResp map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&regResp)
		require.NoError(t, err)
		require.NotNil(t, regResp["id"], "Registration response missing 'id' field. Response: %+v", regResp)
		agentID := regResp["id"].(string)
		require.NotEmpty(t, agentID)

		detailsResp, err := http.Get(fmt.Sprintf("%s/api/v1/agents/%s", serverURL, agentID))
		require.NoError(t, err)
		defer detailsResp.Body.Close()

		assert.Equal(t, http.StatusOK, detailsResp.StatusCode)

		var details map[string]interface{}
		json.NewDecoder(detailsResp.Body).Decode(&details)
		assert.Equal(t, agentID, details["id"])
		assert.Equal(t, "smoke-test-fp-001", details["fingerprint"])
	})

	t.Run("verifies response structure", func(t *testing.T) {
		registerPayload := map[string]interface{}{
			"fingerprint":     "smoke-test-fp-002",
			"token_id":        "smoke-token-id",
			"token_key":       "smoke-token-key",
			"public_key":      "smoke-public-key",
			"public_key_type": "RSA",
			"tags": []map[string]string{
				{"key": "env", "value": "smoke"},
			},
		}

		body, _ := json.Marshal(registerPayload)
		resp, err := http.Post(serverURL+"/api/v1/agents/register", "application/json", bytes.NewReader(body))
		require.NoError(t, err)
		defer resp.Body.Close()

		var regResp map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&regResp)
		require.NoError(t, err)
		require.NotNil(t, regResp["id"], "Registration response missing 'id' field. Response: %+v", regResp)
		agentID := regResp["id"].(string)

		detailsResp, err := http.Get(fmt.Sprintf("%s/api/v1/agents/%s", serverURL, agentID))
		require.NoError(t, err)
		defer detailsResp.Body.Close()

		var details map[string]interface{}
		json.NewDecoder(detailsResp.Body).Decode(&details)

		assert.NotEmpty(t, details["id"])
		assert.NotEmpty(t, details["fingerprint"])
		assert.NotEmpty(t, details["status"])
		assert.NotEmpty(t, details["last_seen"])
		assert.NotEmpty(t, details["registered_at"])
		assert.NotNil(t, details["tags"])
	})

	t.Run("handles non existent agent", func(t *testing.T) {
		detailsResp, err := http.Get(fmt.Sprintf("%s/api/v1/agents/nonexistent", serverURL))
		require.NoError(t, err)
		defer detailsResp.Body.Close()

		assert.Equal(t, http.StatusNotFound, detailsResp.StatusCode)
	})
}
