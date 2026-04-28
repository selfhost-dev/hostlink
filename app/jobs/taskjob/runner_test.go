package taskjob

import (
	"context"
	"fmt"
	"hostlink/app/services/taskreporter"
	"hostlink/domain/task"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskJobQueuesPollingAndEnqueuedWorkSequentially(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "polling-started")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fetcher := &fakeTaskFetcher{tasks: []task.Task{{
		ID:      "poll-task",
		Command: fmt.Sprintf("printf started > %q; sleep 0.2; printf first", marker),
		Status:  "pending",
	}}}
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger})

	cancelJob := job.Register(ctx, fetcher, reporter)
	defer func() {
		cancelJob()
		job.Shutdown()
	}()
	waitForFile(t, marker)
	if err := job.Enqueue(ctx, task.Task{ID: "ws-task", Command: "printf second", Status: "pending"}); err != nil {
		t.Fatalf("enqueue websocket task: %v", err)
	}
	waitForReports(t, reporter, 2)

	got := reporter.taskIDs()
	want := []string{"poll-task", "ws-task"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("report order = %v, want %v", got, want)
	}
}

func TestTaskJobRunsEnqueuedTasksSequentially(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "first-started")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: noOpTrigger})

	cancelJob := job.Register(ctx, &fakeTaskFetcher{}, reporter)
	defer func() {
		cancelJob()
		job.Shutdown()
	}()
	if err := job.Enqueue(ctx, task.Task{ID: "task-1", Command: fmt.Sprintf("printf started > %q; sleep 0.2; printf first", marker), Status: "pending"}); err != nil {
		t.Fatalf("enqueue first task: %v", err)
	}
	waitForFile(t, marker)
	if err := job.Enqueue(ctx, task.Task{ID: "task-2", Command: "printf second", Status: "pending"}); err != nil {
		t.Fatalf("enqueue second task: %v", err)
	}
	waitForReports(t, reporter, 2)

	got := reporter.taskIDs()
	want := []string{"task-1", "task-2"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("report order = %v, want %v", got, want)
	}
}

func TestTaskJobSuppressesDuplicateQueuedAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: noOpTrigger})
	taskAttempt := task.Task{ID: "task-1", ExecutionAttemptID: "attempt-1", Command: "printf run", Status: "pending"}

	cancelJob := job.Register(ctx, &fakeTaskFetcher{}, reporter)
	defer func() {
		cancelJob()
		job.Shutdown()
	}()
	if err := job.Enqueue(ctx, taskAttempt); err != nil {
		t.Fatalf("enqueue first attempt: %v", err)
	}
	if err := job.Enqueue(ctx, taskAttempt); err != nil {
		t.Fatalf("enqueue duplicate attempt: %v", err)
	}
	waitForReports(t, reporter, 1)
	time.Sleep(100 * time.Millisecond)

	if got := len(reporter.resultsSnapshot()); got != 1 {
		t.Fatalf("report count = %d, want 1", got)
	}
}

func noOpTrigger(ctx context.Context, fn func() error) {}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}

func waitForReports(t *testing.T, reporter *fakeTaskReporter, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(reporter.resultsSnapshot()) >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d reports", count)
}

func (f *fakeTaskReporter) taskIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]string, 0, len(f.taskIDsReported))
	ids = append(ids, f.taskIDsReported...)
	return ids
}

func (f *fakeTaskReporter) resultsSnapshot() []*taskreporter.TaskResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	results := make([]*taskreporter.TaskResult, 0, len(f.results))
	results = append(results, f.results...)
	return results
}
