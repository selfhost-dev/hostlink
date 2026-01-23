//go:build linux

package update

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// isProcessAlive checks if a process with the given PID is alive.
// It sends signal 0 to the process - this doesn't actually send a signal
// but performs error checking to determine if the process exists.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}

	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}

	// Signal 0 is a special signal that performs error checking without
	// actually sending a signal. If the process exists and we have
	// permission to signal it, err will be nil.
	err = process.Signal(syscall.Signal(0))
	return err == nil
}

// getProcessStartTime returns the process start time in clock ticks since boot.
// Reads from /proc/[pid]/stat field 22 (starttime).
func getProcessStartTime(pid int) (int64, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid PID: %d", pid)
	}

	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	content, err := os.ReadFile(statPath)
	if err != nil {
		return 0, fmt.Errorf("failed to read %s: %w", statPath, err)
	}

	// /proc/[pid]/stat format has comm (field 2) in parentheses which may contain spaces
	// Find the last ')' to reliably parse fields after comm
	data := string(content)
	closeParen := strings.LastIndex(data, ")")
	if closeParen == -1 {
		return 0, fmt.Errorf("invalid format in %s: no closing parenthesis", statPath)
	}

	// Fields after ')' are space-separated, starting at field 3
	// Field 22 is starttime, so we need field index 19 (22 - 3 = 19) after the ')'
	fieldsAfterComm := strings.Fields(data[closeParen+1:])
	if len(fieldsAfterComm) < 20 {
		return 0, fmt.Errorf("invalid format in %s: not enough fields (got %d)", statPath, len(fieldsAfterComm))
	}

	// starttime is field 22, which is index 19 in fieldsAfterComm (0-indexed, starting from field 3)
	starttime, err := strconv.ParseInt(fieldsAfterComm[19], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse starttime: %w", err)
	}

	return starttime, nil
}

// getCurrentProcessStartTime returns the current process start time.
func getCurrentProcessStartTime() (int64, error) {
	return getProcessStartTime(os.Getpid())
}
