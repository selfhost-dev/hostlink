package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hostlink/app/services/localtaskstore"
)

func TestLogStaleLoopState_ReportsRecoveredState(t *testing.T) {
	var lines []string
	logf := func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}

	snap := localtaskstore.Snapshot{
		RunningTask: &localtaskstore.RunningTaskSnapshot{
			TaskID:             "task_1",
			ExecutionAttemptID: "attempt_1",
		},
		ReceivedNotStarted: []localtaskstore.ReceivedNotStartedAttempt{{TaskID: "task_2"}},
		UnackedFinals:      []localtaskstore.UnackedFinalSnapshot{{MessageID: "m1"}},
		UnackedOutput:      []localtaskstore.UnackedOutputRange{{Stream: "stdout", FirstSequence: 1}},
	}

	logStaleLoopState(snap, logf)

	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "1 in-flight task")
	assert.Contains(t, lines[0], "1 received-not-started")
	assert.Contains(t, lines[0], "1 unacked final")
	assert.Contains(t, lines[0], "1 unacked output range")
}

func TestLogStaleLoopState_CleanState(t *testing.T) {
	var lines []string
	logf := func(format string, args ...any) {
		lines = append(lines, fmt.Sprintf(format, args...))
	}

	logStaleLoopState(localtaskstore.Snapshot{}, logf)

	require.Len(t, lines, 1)
	assert.True(t, strings.Contains(lines[0], "0 in-flight task"), lines[0])
	assert.True(t, strings.Contains(lines[0], "0 received-not-started"), lines[0])
}
