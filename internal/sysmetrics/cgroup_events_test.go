package sysmetrics

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── ParseMemoryEvents ────────────────────────────────────────────────────────

func TestParseMemoryEvents_Valid(t *testing.T) {
	data := []byte("low 0\nhigh 0\nmax 156409856\noom 0\noom_kill 0\noom_group_kill 0\n")
	ev := ParseMemoryEvents(data)

	assert.Equal(t, uint64(0), ev.Low)
	assert.Equal(t, uint64(0), ev.High)
	assert.Equal(t, uint64(156409856), ev.Max)
	assert.Equal(t, uint64(0), ev.OOM)
	assert.Equal(t, uint64(0), ev.OOMKill)
	assert.Equal(t, uint64(0), ev.OOMGroupKill)
}

func TestParseMemoryEvents_NonZeroCounters(t *testing.T) {
	data := []byte("low 12\nhigh 345\noom 2\noom_kill 1\noom_group_kill 0\n")
	ev := ParseMemoryEvents(data)

	assert.Equal(t, uint64(12), ev.Low)
	assert.Equal(t, uint64(345), ev.High)
	assert.Equal(t, uint64(0), ev.Max) // absent key → zero
	assert.Equal(t, uint64(2), ev.OOM)
	assert.Equal(t, uint64(1), ev.OOMKill)
}

func TestParseMemoryEvents_Empty(t *testing.T) {
	ev := ParseMemoryEvents(nil)
	assert.Equal(t, uint64(0), ev.Low)
	assert.Equal(t, uint64(0), ev.High)
	assert.Equal(t, uint64(0), ev.Max)
	assert.Equal(t, uint64(0), ev.OOM)
	assert.Equal(t, uint64(0), ev.OOMKill)
	assert.Equal(t, uint64(0), ev.OOMGroupKill)
}

func TestParseMemoryEvents_Malformed(t *testing.T) {
	ev := ParseMemoryEvents([]byte("garbage\nnot a key value\nmax notanumber\n"))
	assert.Equal(t, uint64(0), ev.Max)
	assert.Equal(t, uint64(0), ev.Low)
}

func TestParseMemoryEvents_UnknownKeysIgnored(t *testing.T) {
	ev := ParseMemoryEvents([]byte("future_key 999\nlow 7\n"))
	assert.Equal(t, uint64(7), ev.Low)
}

// ── Collector with real files on disk ────────────────────────────────────────

// writeMemoryEvents writes a memory.events fixture into tmp/system.slice/<unit>/.
func writeMemoryEvents(t *testing.T, root, unit, content string) {
	t.Helper()
	dir := filepath.Join(root, "system.slice", unit)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "memory.events"), []byte(content), 0o644))
}

func TestCollect_MemoryEventsPopulated(t *testing.T) {
	root := t.TempDir()
	writeMemoryEvents(t, root, "hostlink.service", "low 0\nhigh 1\nmax 512000000\noom 0\noom_kill 0\noom_group_kill 0\n")
	writeMemoryEvents(t, root, "selfhost-postgresql.service", "low 0\nhigh 0\nmax 999\noom 3\noom_kill 1\noom_group_kill 0\n")

	c := NewWithConfig(&Config{CgroupRoot: root})
	m, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, uint64(512000000), m.HostlinkMemoryEvents.Max)
	assert.Equal(t, uint64(3), m.PostgresMemoryEvents.OOM)
	assert.Equal(t, uint64(1), m.PostgresMemoryEvents.OOMKill)
}

func TestCollect_MissingUnitFile_ReturnsZeros(t *testing.T) {
	root := t.TempDir()
	writeMemoryEvents(t, root, "hostlink.service", "low 0\nhigh 0\nmax 100\noom 0\noom_kill 0\n")

	c := NewWithConfig(&Config{CgroupRoot: root})
	m, err := c.Collect(context.Background())

	assert.NoError(t, err)
	// selfhost-postgresql.service absent → zeros, no error
	assert.Equal(t, uint64(0), m.PostgresMemoryEvents.Max)
	assert.Equal(t, uint64(0), m.PostgresMemoryEvents.OOMKill)
	// hostlink.service present → parsed
	assert.Equal(t, uint64(100), m.HostlinkMemoryEvents.Max)
}

func TestCollect_CgroupV1NoMemoryEvents_ReturnsZeros(t *testing.T) {
	root := t.TempDir() // empty tree — no memory.events anywhere (cgroup v1 layout)
	c := NewWithConfig(&Config{CgroupRoot: root})
	m, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, uint64(0), m.HostlinkMemoryEvents.Max)
	assert.Equal(t, uint64(0), m.PostgresMemoryEvents.Max)
}

func TestCollect_MalformedMemoryEvents_ReturnsZerosNoError(t *testing.T) {
	root := t.TempDir()
	writeMemoryEvents(t, root, "hostlink.service", "max boom\n")

	c := NewWithConfig(&Config{CgroupRoot: root})
	m, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, uint64(0), m.HostlinkMemoryEvents.Max)
}

func TestCollect_OtherFailuresStillCollected(t *testing.T) {
	mock := &mockSystemCollector{
		cpuErr:  errors.New("cpu error"),
		memStats: MemoryStats{UsedPercent: 45.0},
	}
	root := t.TempDir()
	writeMemoryEvents(t, root, "hostlink.service", "low 0\nhigh 0\nmax 42\noom 0\noom_kill 0\n")

	c := NewWithConfig(&Config{Collector: mock, CgroupRoot: root})
	m, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, uint64(42), m.HostlinkMemoryEvents.Max)
	assert.Equal(t, 45.0, m.MemoryPercent)
}

// ── Own-cgroup fallback (non-systemd environments) ──────────────────────────

func TestCollect_OwnCgroupFallback(t *testing.T) {
	root := t.TempDir()
	// No hostlink.service unit dir; own cgroup path resolves under root.
	ownDir := filepath.Join(root, "docker", "abc123")
	require.NoError(t, os.MkdirAll(ownDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(ownDir, "memory.events"), []byte("low 0\nhigh 0\nmax 777\noom 0\noom_kill 0\n"), 0o644))

	c := NewWithConfig(&Config{CgroupRoot: root})
	c.(*collector).ownCgroupPath = "docker/abc123"
	m, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, uint64(777), m.HostlinkMemoryEvents.Max)
}

func TestCollect_OwnCgroupFallbackMissing(t *testing.T) {
	root := t.TempDir() // nothing at all
	c := NewWithConfig(&Config{CgroupRoot: root})
	c.(*collector).ownCgroupPath = "docker/nope"
	m, err := c.Collect(context.Background())

	assert.NoError(t, err)
	assert.Equal(t, uint64(0), m.HostlinkMemoryEvents.Max)
}
