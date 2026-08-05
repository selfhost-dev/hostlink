package taskjob

import (
	"os"
	"os/exec"
	"strconv"
	"sync/atomic"
)

// systemdRunPath is resolved once; empty means fall back to plain /bin/sh -c.
var systemdRunPath = resolveSystemdRun()

// taskUnitCounter keeps scope unit names unique across concurrent tasks.
var taskUnitCounter atomic.Int64

func resolveSystemdRun() string {
	path, err := exec.LookPath("systemd-run")
	if err != nil {
		return ""
	}
	return path
}

// buildTaskCmd returns the command that executes a task script. When
// systemd-run is available, the script runs in a transient systemd scope so it
// is NOT charged against the hostlink.service cgroup limits (MemoryMax etc.)
// and survives an agent restart. Otherwise it falls back to plain
// /bin/sh -c (containers, macOS dev) — identical to previous behavior.
func buildTaskCmd(scriptPath string) *exec.Cmd {
	if systemdRunPath == "" {
		return exec.Command("/bin/sh", "-c", scriptPath)
	}
	unit := "hostlink-task-" + strconv.Itoa(os.Getpid()) + "-" + strconv.FormatInt(taskUnitCounter.Add(1), 10)
	return exec.Command(systemdRunPath, "--scope", "--quiet", "--unit="+unit, "/bin/sh", "-c", scriptPath)
}
