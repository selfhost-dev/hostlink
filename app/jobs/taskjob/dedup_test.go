package taskjob

import (
	"context"
	"hostlink/domain/task"
	"testing"
	"time"
)

// TestEnqueueSkipsDuplicateWhileTaskIsQueuedOrRunning reproduces the
// production bug: a task delivered without an execution_attempt_id (e.g.
// via HTTP polling or a heartbeat response) never reports "started" back to
// the control plane, so it keeps looking "pending" to every caller that
// re-checks it. Heartbeat re-checks every 5s and, prior to this fix, called
// Enqueue unconditionally — piling up duplicate, not-yet-started copies of
// the same task that the single consumer goroutine would later run one
// after another, long after the task had actually completed.
func TestEnqueueSkipsDuplicateWhileTaskIsQueuedOrRunning(t *testing.T) {
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger})
	slowTask := task.Task{ID: "dup-task", Command: "sleep 0.1", Status: "pending"}

	// Simulates repeated heartbeat ticks (or polling fetches) all seeing the
	// same task as "pending" while the first enqueue is still queued/running.
	for i := 0; i < 5; i++ {
		if err := job.Enqueue(context.Background(), slowTask); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	cancel := job.Register(context.Background(), &fakeTaskFetcher{}, reporter)
	defer func() {
		cancel()
		job.Shutdown()
	}()

	waitForReports(t, reporter, 1)
	time.Sleep(50 * time.Millisecond) // give any (wrongly) queued duplicates a chance to also run

	results := reporter.resultsSnapshot()
	if len(results) != 1 {
		t.Fatalf("report count = %d, want 1 (5 duplicate Enqueue calls for the same in-flight task must only run once)", len(results))
	}
}

// TestEnqueueAllowsReprocessingAfterTaskCompletes ensures the dedup guard
// only blocks duplicates while a task is queued or running — once it has
// finished (and released its reservation), a later Enqueue for the same
// task ID (e.g. a legitimate retry) must still be accepted.
func TestEnqueueAllowsReprocessingAfterTaskCompletes(t *testing.T) {
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger})
	quickTask := task.Task{ID: "seq-task", Command: "printf ok", Status: "pending"}

	cancel := job.Register(context.Background(), &fakeTaskFetcher{}, reporter)
	defer func() {
		cancel()
		job.Shutdown()
	}()

	if err := job.Enqueue(context.Background(), quickTask); err != nil {
		t.Fatalf("enqueue 1: %v", err)
	}
	waitForReports(t, reporter, 1)

	if err := job.Enqueue(context.Background(), quickTask); err != nil {
		t.Fatalf("enqueue 2: %v", err)
	}
	waitForReports(t, reporter, 2)

	results := reporter.resultsSnapshot()
	if len(results) != 2 {
		t.Fatalf("report count = %d, want 2 (non-overlapping re-enqueue of the same task ID after completion must run again)", len(results))
	}
}
