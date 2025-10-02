//go:build smoke

package smoke

import (
	"encoding/json"
	"hostlink/domain/agent"
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func getServerURL() string {
	if url := os.Getenv("HOSTLINK_SERVER_URL"); url != "" {
		return url
	}
	return "http://localhost:8080"
}

func TestAgentListSmoke(t *testing.T) {
	serverURL := getServerURL()

	t.Run("can list agents from running server", func(t *testing.T) {
		resp, err := http.Get(serverURL + "/api/v1/agents")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		err = json.NewDecoder(resp.Body).Decode(&agents)
		require.NoError(t, err)
	})

	t.Run("can filter agents by status from running server", func(t *testing.T) {
		resp, err := http.Get(serverURL + "/api/v1/agents?status=active")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		err = json.NewDecoder(resp.Body).Decode(&agents)
		require.NoError(t, err)

		for _, a := range agents {
			assert.Equal(t, "active", a.Status)
		}
	})

	t.Run("can filter agents by fingerprint from running server", func(t *testing.T) {
		resp, err := http.Get(serverURL + "/api/v1/agents?fingerprint=specific-fingerprint")
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var agents []agent.Agent
		err = json.NewDecoder(resp.Body).Decode(&agents)
		require.NoError(t, err)

		for _, a := range agents {
			assert.Equal(t, "specific-fingerprint", a.Fingerprint)
		}
	})
}
