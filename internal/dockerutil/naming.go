package dockerutil

import (
	"strings"
)

// ResolveContainerName returns a human-readable name for a container by 
// prioritizing Coolify labels, then Docker Compose labels, then the Docker 
// container name, and finally falling back to a truncated container ID.
func ResolveContainerName(id string, names []string, labels map[string]string) string {
	if coolName, ok := labels["coolify.name"]; ok && coolName != "" {
		return coolName
	}
	if composeService, ok := labels["com.docker.compose.service"]; ok && composeService != "" {
		return composeService
	}
	if len(names) > 0 {
		return strings.TrimPrefix(names[0], "/")
	}
	if len(id) >= 12 {
		return id[:12]
	}
	return id
}
