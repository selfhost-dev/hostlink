package apiserver

import (
	"testing"
	"time"

	"hostlink/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHeartbeatClient_UsesHeartbeatTimeout(t *testing.T) {
	testutil.SetupAgentIdentity(t)
	t.Setenv("HOSTLINK_API_HEARTBEAT_TIMEOUT", "")
	t.Setenv("HOSTLINK_API_SLOW_REQUEST_THRESHOLD", "")

	c, err := NewHeartbeatClient()
	require.NoError(t, err)

	assert.Equal(t, 10*time.Second, c.httpClient.Timeout)
}

func TestNewDefaultClient_KeepsDefaultTimeout(t *testing.T) {
	testutil.SetupAgentIdentity(t)
	t.Setenv("HOSTLINK_API_HEARTBEAT_TIMEOUT", "")
	t.Setenv("HOSTLINK_API_SLOW_REQUEST_THRESHOLD", "")

	c, err := NewDefaultClient()
	require.NoError(t, err)

	assert.Equal(t, 30*time.Second, c.httpClient.Timeout)
}
