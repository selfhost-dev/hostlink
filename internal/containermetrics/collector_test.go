package containermetrics

import (
	"hostlink/internal/dockerutil"
	"testing"
)

func TestResolveContainerName(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		names    []string
		labels   map[string]string
		expected string
	}{
		{
			name:     "prefer coolify name label",
			id:       "12345678901234567890",
			names:    []string{"/docker-name"},
			labels:   map[string]string{"coolify.name": "my-cool-app"},
			expected: "my-cool-app",
		},
		{
			name:     "fallback to compose service label",
			id:       "12345678901234567890",
			names:    []string{"/docker-name"},
			labels:   map[string]string{"com.docker.compose.service": "web-service"},
			expected: "web-service",
		},
		{
			name:     "fallback to docker name",
			id:       "12345678901234567890",
			names:    []string{"/docker-name"},
			labels:   map[string]string{},
			expected: "docker-name",
		},
		{
			name:     "fallback to truncated id",
			id:       "12345678901234567890",
			names:    []string{},
			labels:   map[string]string{},
			expected: "123456789012",
		},
		{
			name:     "short id no fallback",
			id:       "abc",
			names:    []string{},
			labels:   map[string]string{},
			expected: "abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dockerutil.ResolveContainerName(tt.id, tt.names, tt.labels)
			if got != tt.expected {
				t.Errorf("ResolveContainerName() = %v, want %v", got, tt.expected)
			}
		})
	}
}
