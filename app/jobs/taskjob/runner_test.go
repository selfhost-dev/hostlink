package taskjob

import (
	"context"
	"hostlink/app/services/taskreporter"
	"hostlink/domain/task"
	"testing"
	"time"
)

func TestTaskJobSkipsPollingFetchWhenPollingGateDisabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fetcher := &fakeTaskFetcher{tasks: []task.Task{{ID: "poll-task", Command: "printf poll", Status: "pending"}}}
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger, PollingGate: fakePollingGate{shouldPoll: false}})

	cancelJob := job.Register(ctx, fetcher, reporter)
	defer func() {
		cancelJob()
		job.Shutdown()
	}()
	time.Sleep(50 * time.Millisecond)

	if fetcher.callCount() != 0 {
		t.Fatalf("fetch count = %d, want 0", fetcher.callCount())
	}
	if got := len(reporter.resultsSnapshot()); got != 0 {
		t.Fatalf("report count = %d, want 0", got)
	}
}

func TestTaskJobRunsPollingFetchWhenPollingGateEnabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fetcher := &fakeTaskFetcher{tasks: []task.Task{{ID: "poll-task", Command: "printf poll", Status: "pending"}}}
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger, PollingGate: fakePollingGate{shouldPoll: true}})

	cancelJob := job.Register(ctx, fetcher, reporter)
	defer func() {
		cancelJob()
		job.Shutdown()
	}()
	waitForReports(t, reporter, 1)

	if fetcher.callCount() != 1 {
		t.Fatalf("fetch count = %d, want 1", fetcher.callCount())
	}
}

type fakePollingGate struct {
	shouldPoll bool
}

func (f fakePollingGate) ShouldPoll() bool {
	return f.shouldPoll
}

func (f *fakeTaskReporter) resultsSnapshot() []*taskreporter.TaskResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	results := make([]*taskreporter.TaskResult, len(f.results))
	copy(results, f.results)
	return results
}

func waitForReports(t *testing.T, reporter *fakeTaskReporter, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(reporter.resultsSnapshot()) >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d reports", count)
}
