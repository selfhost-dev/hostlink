package taskjob

import (
	"context"
	"hostlink/domain/task"
	"testing"
	"time"
)

// TestProcessTaskSkipsConcurrentDuplicateForSameTaskID reproduces the
// production bug where the same task could be dispatched twice concurrently —
// once via the WebSocket-enqueue consumer and once via the independent HTTP
// polling trigger, which keeps re-fetching a task as long as the control
// plane still reports it as not "completed" (which is true for the entire
// duration the command is running). Without an in-flight guard, both paths
// call processTask and spawn a second, overlapping execution of the same
// command against the same task.
func TestProcessTaskSkipsConcurrentDuplicateForSameTaskID(t *testing.T) {
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger})
	slowTask := task.Task{ID: "dup-task", ExecutionAttemptID: "attempt-1", Command: "sleep 0.1", Status: "pending"}

	done := make(chan struct{})
	go func() {
		defer close(done)
		job.processTask(context.Background(), slowTask, reporter, nil)
	}()

	// Give the first call time to acquire the in-flight lock and start
	// executing before the "duplicate dispatch" arrives.
	time.Sleep(20 * time.Millisecond)

	// Simulates a second, concurrent dispatch of the exact same task (e.g.
	// the polling trigger fetching it while the WebSocket-enqueued run above
	// is still in flight). This must return immediately without re-running
	// the command.
	start := time.Now()
	job.processTask(context.Background(), slowTask, reporter, nil)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("duplicate processTask call took %v, want a near-instant skip", elapsed)
	}

	<-done

	results := reporter.resultsSnapshot()
	if len(results) != 1 {
		t.Fatalf("report count = %d, want 1 (duplicate concurrent dispatch must not re-run the command)", len(results))
	}
}

// TestProcessTaskAllowsSequentialReprocessingAfterCompletion ensures the
// in-flight guard only blocks true concurrency — once a task's execution has
// finished, a later (non-overlapping) dispatch of the same task ID must still
// be allowed to run, e.g. for legitimate retries.
func TestProcessTaskAllowsSequentialReprocessingAfterCompletion(t *testing.T) {
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger})
	quickTask := task.Task{ID: "seq-task", Command: "printf ok", Status: "pending"}

	job.processTask(context.Background(), quickTask, reporter, nil)
	job.processTask(context.Background(), quickTask, reporter, nil)

	results := reporter.resultsSnapshot()
	if len(results) != 2 {
		t.Fatalf("report count = %d, want 2 (non-overlapping dispatches of the same task ID must both run)", len(results))
	}
}
