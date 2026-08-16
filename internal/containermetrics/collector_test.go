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
			name:     "prefer compose service over coolify name",
			id:       "12345678901234567890",
			names:    []string{"/docker-name"},
			labels:   map[string]string{"com.docker.compose.service": "web-service", "coolify.name": "uuid-resource-id"},
			expected: "web-service",
		},
		{
			name:     "fallback to coolify name when compose not set",
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

func TestCoolifyServiceID(t *testing.T) {
	tests := []struct {
		name     string
		labels   map[string]string
		expected string
	}{
		{
			name:     "prefers coolify.serviceId label",
			labels:   map[string]string{"coolify.serviceId": "svcuuid123", "com.docker.compose.project": "projxyz"},
			expected: "svcuuid123",
		},
		{
			name:     "falls back to compose project (Coolify uses the service uuid as the project)",
			labels:   map[string]string{"com.docker.compose.project": "twentyuuid456"},
			expected: "twentyuuid456",
		},
		{
			name:     "empty when neither present (non-service container)",
			labels:   map[string]string{"coolify.name": "twenty", "com.docker.compose.service": "twenty"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := coolifyServiceID(tt.labels); got != tt.expected {
				t.Errorf("coolifyServiceID() = %q, want %q", got, tt.expected)
			}
		})
	}
}
