package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_Default(t *testing.T) {
	os.Unsetenv("HOSTLINK_SERVER_URL")

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "http://localhost:8080", cfg.GetServerURL())
}

func TestLoad_EnvVar(t *testing.T) {
	os.Setenv("HOSTLINK_SERVER_URL", "http://example.com:9090")
	defer os.Unsetenv("HOSTLINK_SERVER_URL")

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "http://example.com:9090", cfg.GetServerURL())
}

func TestGetServerURL_Priority(t *testing.T) {
	t.Run("env var takes precedence over config file", func(t *testing.T) {
		os.Setenv("HOSTLINK_SERVER_URL", "http://env.com")
		defer os.Unsetenv("HOSTLINK_SERVER_URL")

		cfg := &Config{
			ServerURL: "http://file.com",
		}

		assert.Equal(t, "http://env.com", cfg.GetServerURL())
	})

	t.Run("config file takes precedence over default", func(t *testing.T) {
		os.Unsetenv("HOSTLINK_SERVER_URL")

		cfg := &Config{
			ServerURL: "http://file.com",
		}

		assert.Equal(t, "http://file.com", cfg.GetServerURL())
	})

	t.Run("default when no env var or config", func(t *testing.T) {
		os.Unsetenv("HOSTLINK_SERVER_URL")

		cfg := &Config{
			ServerURL: "",
		}

		assert.Equal(t, "http://localhost:8080", cfg.GetServerURL())
	})
}
