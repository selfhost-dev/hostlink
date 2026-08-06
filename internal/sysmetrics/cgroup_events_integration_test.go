//go:build integration

package sysmetrics

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Real-host checks: cgroup v2 systemd host with hostlink.service present.
func TestIntegration_RealCgroupEvents(t *testing.T) {
	require.DirExists(t, "/sys/fs/cgroup/system.slice", "host must be cgroup v2")
	c := NewWithConfig(&Config{})
	m, err := c.Collect(context.Background())
	require.NoError(t, err)

	assert.FileExists(t, "/sys/fs/cgroup/system.slice/hostlink.service/memory.events")
	if _, err := os.Stat("/sys/fs/cgroup/system.slice/hostlink.service/memory.events"); err == nil {
		t.Logf("hostlink memory.events: max=%d oom=%d oom_kill=%d",
			m.HostlinkMemoryEvents.Max, m.HostlinkMemoryEvents.OOM, m.HostlinkMemoryEvents.OOMKill)
	}
	t.Logf("own cgroup path: %s", c.(*collector).ownCgroupPath)
}

// Real-host check: /proc/self/cgroup resolves to a v2 path we can read.
func TestIntegration_OwnCgroupReadable(t *testing.T) {
	c := NewWithConfig(&Config{})
	path := c.(*collector).ownCgroupPath
	require.NotEmpty(t, path)
	data, err := os.ReadFile("/sys/fs/cgroup/" + path + "/memory.events")
	require.NoError(t, err)
	ev := ParseMemoryEvents(data)
	t.Logf("own memory.events: max=%d", ev.Max)
}
