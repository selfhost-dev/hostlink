package config

import (
	"os"
	"path/filepath"

	"github.com/goccy/go-yaml"
)

const (
	defaultServerURL = "http://localhost:8080"
	envVarServerURL  = "HOSTLINK_SERVER_URL"
	configFileName   = ".hostlink/config.yml"
)

// Config holds the hlctl configuration
type Config struct {
	ServerURL string `yaml:"server"`
}

// Load loads configuration from file and environment
func Load() (*Config, error) {
	cfg := &Config{}

	// Try to load from config file
	if err := loadFromFile(cfg); err != nil {
		// Ignore file not found errors, use defaults
	}

	return cfg, nil
}

// GetServerURL returns the server URL with priority: env var > config file > default
func (c *Config) GetServerURL() string {
	// Priority 1: Environment variable
	if url := os.Getenv(envVarServerURL); url != "" {
		return url
	}

	// Priority 2: Config file
	if c.ServerURL != "" {
		return c.ServerURL
	}

	// Priority 3: Default
	return defaultServerURL
}

// loadFromFile loads configuration from ~/.hostlink/config.yml
func loadFromFile(cfg *Config) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(homeDir, configFileName)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(data, cfg)
}
