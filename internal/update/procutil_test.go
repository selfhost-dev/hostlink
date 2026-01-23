package update

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsProcessAlive_CurrentProcess(t *testing.T) {
	// Current process should always be alive
	alive := isProcessAlive(os.Getpid())
	assert.True(t, alive, "current process should be alive")
}

func TestIsProcessAlive_InvalidPID(t *testing.T) {
	// Negative PID should not be alive
	alive := isProcessAlive(-1)
	assert.False(t, alive, "negative PID should not be alive")
}

func TestIsProcessAlive_NonExistentPID(t *testing.T) {
	// Very large PID that almost certainly doesn't exist
	alive := isProcessAlive(99999999)
	assert.False(t, alive, "non-existent PID should not be alive")
}

func TestGetProcessStartTime_CurrentProcess(t *testing.T) {
	startTime, err := getProcessStartTime(os.Getpid())
	require.NoError(t, err)
	assert.Greater(t, startTime, int64(0), "start time should be positive")
}

func TestGetProcessStartTime_InvalidPID(t *testing.T) {
	_, err := getProcessStartTime(-1)
	assert.Error(t, err, "should error for invalid PID")
}

func TestGetProcessStartTime_NonExistentPID(t *testing.T) {
	_, err := getProcessStartTime(99999999)
	assert.Error(t, err, "should error for non-existent PID")
}

func TestGetProcessStartTime_Consistency(t *testing.T) {
	// Calling twice should return the same value
	pid := os.Getpid()
	startTime1, err := getProcessStartTime(pid)
	require.NoError(t, err)

	startTime2, err := getProcessStartTime(pid)
	require.NoError(t, err)

	assert.Equal(t, startTime1, startTime2, "start time should be consistent")
}

func TestGetCurrentProcessStartTime(t *testing.T) {
	startTime, err := getCurrentProcessStartTime()
	require.NoError(t, err)
	assert.Greater(t, startTime, int64(0), "start time should be positive")

	// Should match getProcessStartTime for current PID
	expected, err := getProcessStartTime(os.Getpid())
	require.NoError(t, err)
	assert.Equal(t, expected, startTime)
}
