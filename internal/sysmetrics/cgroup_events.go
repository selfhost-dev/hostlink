package sysmetrics

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"hostlink/domain/metrics"
)

// Units whose cgroup v2 memory pressure counters are tracked. The
// selfhost-postgresql.service entry is the DB lifecycle unit managed by the
// control plane; hostlink.service is the agent's own unit.
var trackedUnits = []string{"hostlink.service", "selfhost-postgresql.service"}

// ParseMemoryEvents parses the contents of a cgroup v2 memory.events file.
// Unknown keys are ignored and absent keys parse to zero, so the function
// never fails for partial or future kernel formats.
func ParseMemoryEvents(data []byte) metrics.CgroupMemoryEvents {
	var ev metrics.CgroupMemoryEvents
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		switch fields[0] {
		case "low":
			ev.Low = v
		case "high":
			ev.High = v
		case "max":
			ev.Max = v
		case "oom":
			ev.OOM = v
		case "oom_kill":
			ev.OOMKill = v
		case "oom_group_kill":
			ev.OOMGroupKill = v
		}
	}
	return ev
}

// readMemoryEvents reads and parses memory.events from a cgroup directory.
// Any error (missing file, permission, malformed data) yields zero counters —
// cgroup pressure telemetry must never fail the collection cycle.
func readMemoryEvents(cgroupDir string) metrics.CgroupMemoryEvents {
	data, err := os.ReadFile(filepath.Join(cgroupDir, "memory.events"))
	if err != nil {
		return metrics.CgroupMemoryEvents{}
	}
	return ParseMemoryEvents(data)
}

// ownCgroupPath returns the process's cgroup v2 path (e.g. "docker/abc123")
// parsed from /proc/self/cgroup, or "" when unavailable (non-Linux, cgroup v1).
func ownCgroupPath() string {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		// cgroup v2 lines look like "0::/system.slice/hostlink.service"
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[1] == "" {
			return strings.TrimPrefix(parts[2], "/")
		}
	}
	return ""
}

func (c *collector) collectMemoryEvents(root string) (hostlink, postgres metrics.CgroupMemoryEvents) {
	for _, unit := range trackedUnits {
		dir := filepath.Join(root, "system.slice", unit)
		if _, err := os.Stat(filepath.Join(dir, "memory.events")); err != nil {
			// Unit absent (fresh install, no DB) or cgroup v1 host — skip.
			continue
		}
		ev := readMemoryEvents(dir)
		switch unit {
		case "hostlink.service":
			hostlink = ev
		case "selfhost-postgresql.service":
			postgres = ev
		}
	}
	// Non-systemd environments (containers): fall back to our own cgroup.
	if hostlink == (metrics.CgroupMemoryEvents{}) && c.ownCgroupPath != "" {
		hostlink = readMemoryEvents(filepath.Join(root, c.ownCgroupPath))
	}
	return hostlink, postgres
}
