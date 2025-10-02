//go:build integration
// +build integration

package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigFile_Create(t *testing.T) {
	tmpDir := t.TempDir()

	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")
	os.Unsetenv("HOSTLINK_SERVER_URL")

	configData := map[string]string{
		"server": "http://test.com:8080",
	}

	data, err := yaml.Marshal(configData)
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Join(tmpDir, ".hostlink"), 0755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, ".hostlink", "config.yml"), data, 0644)
	require.NoError(t, err)

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "http://test.com:8080", cfg.GetServerURL())
}

func TestConfigFile_Read(t *testing.T) {
	tmpDir := t.TempDir()

	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")
	os.Unsetenv("HOSTLINK_SERVER_URL")

	configData := map[string]string{
		"server": "http://configured.com:9090",
	}

	data, err := yaml.Marshal(configData)
	require.NoError(t, err)

	err = os.MkdirAll(filepath.Join(tmpDir, ".hostlink"), 0755)
	require.NoError(t, err)

	err = os.WriteFile(filepath.Join(tmpDir, ".hostlink", "config.yml"), data, 0644)
	require.NoError(t, err)

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "http://configured.com:9090", cfg.GetServerURL())
}

func TestConfigFile_NotExists(t *testing.T) {
	tmpDir := t.TempDir()

	os.Setenv("HOME", tmpDir)
	defer os.Unsetenv("HOME")
	os.Unsetenv("HOSTLINK_SERVER_URL")

	cfg, err := Load()
	require.NoError(t, err)
	require.NotNil(t, cfg)

	assert.Equal(t, "http://localhost:8080", cfg.GetServerURL())
}
