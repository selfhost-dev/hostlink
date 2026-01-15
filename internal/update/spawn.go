package update

import (
	"os/exec"
	"syscall"
)

// SpawnUpdater starts the updater binary in its own process group.
// The updater survives the agent's shutdown because Setpgid: true
// places it in a new process group that systemd won't kill.
// This is fire-and-forget: the caller does not wait for the process to exit.
func SpawnUpdater(updaterPath string, args []string) error {
	cmd, err := spawnWithCmd(updaterPath, args)
	if err != nil {
		return err
	}
	_ = cmd // Process started; caller does not manage it.
	return nil
}

// spawnWithCmd is the internal implementation that returns the exec.Cmd
// for testing purposes (to inspect the child PID/PGID).
func spawnWithCmd(updaterPath string, args []string) (*exec.Cmd, error) {
	cmd := exec.Command(updaterPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
