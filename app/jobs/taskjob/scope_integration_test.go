package taskjob

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireSystemd skips when systemd-run is absent or the systemd bus is not
// reachable (containers, macOS dev). These tests exercise real scope cgroups
// and are the mechanism-level half of the cgroup-isolation fix (#227); the
// service-level half runs on UAT with the agent as a systemd service.
func requireSystemd(t *testing.T) {
	t.Helper()
	if systemdRunPath == "" {
		t.Skip("systemd-run not available")
	}
	probe := exec.Command(systemdRunPath, "--scope", "--quiet", "/bin/true")
	if err := probe.Run(); err != nil {
		t.Skipf("systemd bus not reachable: %v", err)
	}
}

// scopeChildPid returns the first descendant PID of systemd-run's process
// (the /bin/sh -c process that lives inside the scope).
func scopeChildPid(t *testing.T, parent int) int {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		children := readChildren(t, parent)
		if len(children) > 0 {
			return children[0]
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no child of systemd-run pid %d appeared", parent)
	return 0
}

func readChildren(t *testing.T, pid int) []int {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/task/%d/children", pid, pid))
	if err != nil {
		return nil
	}
	var out []int
	sc := bufio.NewScanner(strings.NewReader(string(data)))
	sc.Split(bufio.ScanWords)
	for sc.Scan() {
		n, err := strconv.Atoi(sc.Text())
		if err == nil {
			out = append(out, n)
		}
	}
	return out
}

func procCgroup(t *testing.T, pid int) string {
	t.Helper()
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", pid))
	require.NoError(t, err)
	return strings.TrimSpace(string(data))
}

func procAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// TestScope_SurvivesParentDeath - the scope is owned by systemd, so killing
// systemd-run (e.g. agent OOM/restart) must NOT kill the in-flight task.
func TestScope_SurvivesParentDeath(t *testing.T) {
	requireSystemd(t)

	cmd := buildTaskCmd("/bin/sh -c 'sleep 30'")
	require.NoError(t, cmd.Start())
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	shellPid := scopeChildPid(t, cmd.Process.Pid)
	require.True(t, procAlive(shellPid), "task shell must be alive before parent death")

	// Simulate agent death: SIGKILL systemd-run. The scope must survive.
	require.NoError(t, cmd.Process.Kill())
	_, _ = cmd.Process.Wait()

	time.Sleep(500 * time.Millisecond)
	assert.True(t, procAlive(shellPid), "task must survive parent (systemd-run) death")

	// Cleanup: kill the task; the transient scope is GC'd by systemd.
	require.NoError(t, syscall.Kill(shellPid, syscall.SIGKILL))
}

// cgroupAnonMB reads the anon (unreclaimable) memory of a cgroup from
// memory.stat. Page cache is charged to the cgroup of the reader and would
// pollute a memory.current comparison, so we compare anon only.
func cgroupAnonMB(t *testing.T, cgroupPath string) int {
	t.Helper()
	stat, err := os.ReadFile(filepath.Join(cgroupPath, "memory.stat"))
	require.NoError(t, err)
	for _, line := range strings.Split(string(stat), "\n") {
		key, value, ok := strings.Cut(line, " ")
		if ok && key == "anon" {
			n, err := strconv.ParseUint(value, 10, 64)
			require.NoError(t, err)
			return int(n / (1 << 20))
		}
	}
	t.Fatalf("no anon line in memory.stat of %s", cgroupPath)
	return 0
}

// TestScope_MemoryIsolation - a memory-hungry task is charged to its own
// scope cgroup, not to the caller's cgroup (hostlink.service in production).
func TestScope_MemoryIsolation(t *testing.T) {
	requireSystemd(t)

	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	// Allocate 200M anonymous, touch pages, then sleep so the scope persists.
	script := fmt.Sprintf("%s -c \"import time; x=bytearray(200*1024*1024); [x.__setitem__(i,1) for i in range(0,len(x),4096)]; time.sleep(30)\"", python)
	cmd := buildTaskCmd("/bin/sh -c '" + script + "'")
	require.NoError(t, cmd.Start())
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	}()

	shellPid := scopeChildPid(t, cmd.Process.Pid)

	// The task shell must live in its own scope cgroup, not the caller's.
	callerCgroup := procCgroup(t, os.Getpid())
	taskCgroup := procCgroup(t, shellPid)
	assert.NotEqual(t, callerCgroup, taskCgroup, "task must not share the caller cgroup")
	assert.Contains(t, taskCgroup, "hostlink-task-", "task cgroup: %s", taskCgroup)

	callerCgroupPath := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(callerCgroup, "0::"))
	taskCgroupPath := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(taskCgroup, "0::"))
	callerBefore := cgroupAnonMB(t, callerCgroupPath)

	// Wait for the allocator to finish, then the scope's anon memory must
	// reflect the allocation while the caller's stays flat.
	deadline := time.Now().Add(15 * time.Second)
	for {
		if cgroupAnonMB(t, taskCgroupPath) > 150 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("scope anon memory never exceeded 150M")
		}
		time.Sleep(250 * time.Millisecond)
	}

	callerAfter := cgroupAnonMB(t, callerCgroupPath)
	assert.Less(t, callerAfter-callerBefore, 150, "caller cgroup anon must stay flat; task memory is in its scope")

	// Cleanup.
	require.NoError(t, syscall.Kill(shellPid, syscall.SIGKILL))
}
