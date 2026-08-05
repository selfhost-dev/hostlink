package linux_test

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// readUnit loads the shipped hostlink.service unit file.
func readUnit(t *testing.T) map[string]string {
	t.Helper()
	data, err := os.ReadFile("hostlink.service")
	require.NoError(t, err)

	kv := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			kv[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return kv
}

// parseBytes parses "512M" -> bytes (only the suffixes used in this unit).
func parseBytes(t *testing.T, s string) uint64 {
	t.Helper()
	mult := map[string]uint64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30}
	for suffix, m := range mult {
		if strings.HasSuffix(s, suffix) {
			n, err := strconv.ParseUint(strings.TrimSuffix(s, suffix), 10, 64)
			require.NoError(t, err)
			return n * m
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	require.NoError(t, err)
	return n
}

// TestUnitFile_MemoryProtection - the cgroup memory limits must stay sane:
// accounting on, a soft limit above the observed idle usage (~25 MiB incl.
// page cache), and a hard backstop above the soft limit.
func TestUnitFile_MemoryProtection(t *testing.T) {
	kv := readUnit(t)

	assert.Equal(t, "true", kv["MemoryAccounting"], "MemoryAccounting must stay enabled")
	assert.Equal(t, "256M", kv["MemoryHigh"], "soft limit: 256M (idle agent ~25 MiB, ~77 MiB page cache under load)")
	assert.Equal(t, "512M", kv["MemoryMax"], "hard backstop: 512M")

	high, errHigh := strconv.ParseUint(strings.TrimSuffix(kv["MemoryHigh"], "M"), 10, 64)
	max, errMax := strconv.ParseUint(strings.TrimSuffix(kv["MemoryMax"], "M"), 10, 64)
	require.NoError(t, errHigh)
	require.NoError(t, errMax)
	assert.Greater(t, max, high, "MemoryMax must exceed MemoryHigh")
}

// TestUnitFile_TaskScopeCompatibility - the unit must stay compatible with
// tasks running in transient scopes: KillMode=process keeps agent restarts
// from killing in-flight scope tasks, and the watchdog is part of the same
// reliability set.
func TestUnitFile_TaskScopeCompatibility(t *testing.T) {
	kv := readUnit(t)

	assert.Equal(t, "process", kv["KillMode"], "KillMode=process: agent restart must not kill scope tasks")
	assert.Equal(t, "30", kv["WatchdogSec"], "watchdog must stay configured")
	assert.Equal(t, "simple", kv["Type"])
}

// TestUnitFile_SystemdAnalyzeVerify - validate syntax with systemd's own
// verifier when available (Linux with systemd). Skipped elsewhere.
func TestUnitFile_SystemdAnalyzeVerify(t *testing.T) {
	path, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("systemd-analyze not available")
	}
	out, err := exec.Command(path, "verify", "hostlink.service").CombinedOutput()
	assert.NoError(t, err, "systemd-analyze verify output: %s", string(out))
}
