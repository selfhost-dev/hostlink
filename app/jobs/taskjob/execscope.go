package taskjob

import (
	"os"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
)

var (
	// systemdRunOnce guards the one-time probe: systemd-run may be present as
	// a binary without a running systemd (CI containers, dev images), in
	// which case tasks must keep using the plain /bin/sh -c path.
	systemdRunOnce sync.Once
	systemdRunPath string

	// taskUnitCounter keeps scope unit names unique across concurrent tasks.
	taskUnitCounter atomic.Int64
)

// probeSystemdRun returns the systemd-run path only when it actually works:
// the binary must exist AND be able to create a transient scope. Negative
// results are cached for the process lifetime.
func probeSystemdRun() string {
	path, err := exec.LookPath("systemd-run")
	if err != nil {
		return ""
	}
	if err := exec.Command(path, "--scope", "--quiet", "/bin/true").Run(); err != nil {
		return ""
	}
	return path
}

func resolveSystemdRun() string {
	systemdRunOnce.Do(func() {
		systemdRunPath = probeSystemdRun()
	})
	return systemdRunPath
}

// buildTaskCmd returns the command that executes a task script. When a
// working systemd is available, the script runs in a transient systemd scope
// so it is NOT charged against the hostlink.service cgroup limits (MemoryMax
// etc.) and survives an agent restart. Otherwise it falls back to plain
// /bin/sh -c (containers, macOS dev) — identical to previous behavior.
func buildTaskCmd(scriptPath string) *exec.Cmd {
	return buildTaskCmdWithRun(scriptPath, resolveSystemdRun())
}

// buildTaskCmdWithRun is the pure form of buildTaskCmd, testable without a
// real systemd: sdPath is the verified systemd-run path or "" for fallback.
func buildTaskCmdWithRun(scriptPath, sdPath string) *exec.Cmd {
	if sdPath == "" {
		return exec.Command("/bin/sh", "-c", scriptPath)
	}
	unit := "hostlink-task-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(taskUnitCounter.Add(1), 10)
	return exec.Command(sdPath, "--scope", "--quiet", "--unit="+unit, "/bin/sh", "-c", scriptPath)
}
