package taskjob

import (
	"bytes"
	"context"
	"errors"
	"hostlink/app/services/localtaskstore"
	"hostlink/app/services/taskreporter"
	"hostlink/domain/task"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestTaskJobStreamsOutputAndFinalOverResultChannel(t *testing.T) {
	fetcher := &fakeTaskFetcher{tasks: []task.Task{{
		ID:                 "task-1",
		ExecutionAttemptID: "attempt-1",
		Command:            "printf 'out\\n'; printf 'err\\n' >&2",
		Status:             "pending",
	}}}
	reporter := &fakeTaskReporter{}
	channel := &fakeResultChannel{}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger})

	job.processTask(context.Background(), fetcher.tasks[0], reporter, channel)

	if len(channel.started) != 1 {
		t.Fatalf("started len = %d, want 1", len(channel.started))
	}
	if channel.started[0].TaskID != "task-1" || channel.started[0].ExecutionAttemptID != "attempt-1" {
		t.Fatalf("started = %#v", channel.started[0])
	}
	if len(channel.outputs) != 2 {
		t.Fatalf("outputs len = %d, want 2", len(channel.outputs))
	}
	stdout := outputByStream(channel.outputs, "stdout")
	stderr := outputByStream(channel.outputs, "stderr")
	if stdout == nil || stdout.Sequence != 1 || stdout.Payload != "out\n" {
		t.Fatalf("stdout chunk = %#v", stdout)
	}
	if stderr == nil || stderr.Sequence != 1 || stderr.Payload != "err\n" {
		t.Fatalf("stderr chunk = %#v", stderr)
	}
	if len(channel.finals) != 1 {
		t.Fatalf("finals len = %d, want 1", len(channel.finals))
	}
	if channel.finals[0].TaskID != "task-1" || channel.finals[0].ExecutionAttemptID != "attempt-1" || channel.finals[0].Status != "completed" {
		t.Fatalf("final = %#v", channel.finals[0])
	}
	if len(reporter.results) != 0 {
		t.Fatalf("http reports len = %d, want 0 when result channel succeeds", len(reporter.results))
	}
}

func TestTaskJobFallsBackToHTTPReporterWhenResultChannelDisabled(t *testing.T) {
	fetcher := &fakeTaskFetcher{tasks: []task.Task{{ID: "task-1", Command: "printf 'out'", Status: "pending"}}}
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger})

	job.processTask(context.Background(), fetcher.tasks[0], reporter, nil)

	if len(reporter.results) != 1 {
		t.Fatalf("http reports len = %d, want 1", len(reporter.results))
	}
	if reporter.results[0].Output != "out" {
		t.Fatalf("output = %q, want out", reporter.results[0].Output)
	}
}

func TestTaskJobFallsBackToHTTPReporterWhenFinalPersistenceFails(t *testing.T) {
	fetcher := &fakeTaskFetcher{tasks: []task.Task{{
		ID:                 "task-1",
		ExecutionAttemptID: "attempt-1",
		Command:            "printf 'out'",
		Status:             "pending",
	}}}
	reporter := &fakeTaskReporter{}
	channel := &fakeResultChannel{finalErr: errors.New("store down")}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger})

	job.processTask(context.Background(), fetcher.tasks[0], reporter, channel)

	if len(reporter.results) != 1 {
		t.Fatalf("http reports len = %d, want 1", len(reporter.results))
	}
	if len(channel.finals) != 1 {
		t.Fatalf("finals len = %d, want 1", len(channel.finals))
	}
}

func TestTaskJobRecordsStartedBeforeProcessLaunch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "process-started")
	reporter := &fakeTaskReporter{}
	checked := false
	channel := &fakeResultChannel{recordStartedHook: func() error {
		checked = true
		if _, err := os.Stat(marker); err == nil {
			return errors.New("process launched before durable started state")
		}
		return nil
	}}
	job := NewJobWithConf(TaskJobConfig{Trigger: runOnceTrigger})

	job.processTask(context.Background(), task.Task{
		ID:                 "task-1",
		ExecutionAttemptID: "attempt-1",
		Command:            "printf launched > " + marker,
		Status:             "pending",
	}, reporter, channel)

	if !checked {
		t.Fatal("RecordStarted was not called")
	}
	if len(channel.started) != 1 {
		t.Fatalf("started len = %d, want 1", len(channel.started))
	}
}

func TestCaptureStreamFlushesOnByteThreshold(t *testing.T) {
	reader, writer := io.Pipe()
	channel := &fakeResultChannel{}
	job := NewJobWithConf(TaskJobConfig{
		OutputFlushInterval:  time.Hour,
		OutputFlushThreshold: 4,
	})
	done := make(chan struct{})

	go func() {
		var sink bytes.Buffer
		job.captureStream(context.Background(), task.Task{ID: "task-1", ExecutionAttemptID: "attempt-1"}, "stdout", reader, &sink, channel)
		close(done)
	}()

	_, _ = writer.Write([]byte("ab"))
	if len(channel.outputs) != 0 {
		t.Fatalf("outputs len = %d, want no flush before threshold", len(channel.outputs))
	}
	_, _ = writer.Write([]byte("cd"))
	waitForOutputs(t, channel, 1)
	_ = writer.Close()
	<-done

	if channel.outputs[0].Payload != "abcd" {
		t.Fatalf("payload = %q, want abcd", channel.outputs[0].Payload)
	}
}

func TestCaptureStreamFlushesOnInterval(t *testing.T) {
	reader, writer := io.Pipe()
	channel := &fakeResultChannel{}
	job := NewJobWithConf(TaskJobConfig{
		OutputFlushInterval:  10 * time.Millisecond,
		OutputFlushThreshold: 1024,
	})
	done := make(chan struct{})

	go func() {
		var sink bytes.Buffer
		job.captureStream(context.Background(), task.Task{ID: "task-1", ExecutionAttemptID: "attempt-1"}, "stdout", reader, &sink, channel)
		close(done)
	}()

	_, _ = writer.Write([]byte("slow"))
	waitForOutputs(t, channel, 1)
	_ = writer.Close()
	<-done

	if channel.outputs[0].Payload != "slow" {
		t.Fatalf("payload = %q, want slow", channel.outputs[0].Payload)
	}
}

func TestCaptureStreamRetainsChunkWhenPersistFails(t *testing.T) {
	reader, writer := io.Pipe()
	channel := &fakeResultChannel{outputErrs: []error{errors.New("store down"), nil}}
	job := NewJobWithConf(TaskJobConfig{
		OutputFlushInterval:  10 * time.Millisecond,
		OutputFlushThreshold: 1024,
	})
	done := make(chan struct{})

	go func() {
		var sink bytes.Buffer
		job.captureStream(context.Background(), task.Task{ID: "task-1", ExecutionAttemptID: "attempt-1"}, "stdout", reader, &sink, channel)
		close(done)
	}()

	_, _ = writer.Write([]byte("retry"))
	waitForOutputs(t, channel, 2)
	_ = writer.Close()
	<-done

	if channel.outputs[1].Payload != "retry" || channel.outputs[1].Sequence != 1 {
		t.Fatalf("retry output = %#v", channel.outputs[1])
	}
}

func runOnceTrigger(ctx context.Context, fn func() error) {
	_ = fn()
}

type fakeTaskFetcher struct {
	tasks []task.Task
}

func (f *fakeTaskFetcher) Fetch() ([]task.Task, error) {
	return f.tasks, nil
}

type fakeTaskReporter struct {
	mu              sync.Mutex
	taskIDsReported []string
	results         []*taskreporter.TaskResult
}

func (f *fakeTaskReporter) Report(taskID string, result *taskreporter.TaskResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.taskIDsReported = append(f.taskIDsReported, taskID)
	f.results = append(f.results, result)
	return nil
}

type fakeResultChannel struct {
	mu                sync.Mutex
	recordedStarted   []localtaskstore.TaskReceipt
	started           []localtaskstore.TaskReceipt
	outputs           []localtaskstore.OutputChunk
	finals            []localtaskstore.FinalResult
	recordStartedErr  error
	recordStartedHook func() error
	startedErr        error
	outputErrs        []error
	finalErr          error
}

func (f *fakeResultChannel) RecordStarted(ctx context.Context, receipt localtaskstore.TaskReceipt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recordStartedHook != nil {
		if err := f.recordStartedHook(); err != nil {
			return err
		}
	}
	f.recordedStarted = append(f.recordedStarted, receipt)
	return f.recordStartedErr
}

func (f *fakeResultChannel) SendStarted(ctx context.Context, receipt localtaskstore.TaskReceipt) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, receipt)
	return f.startedErr
}

func (f *fakeResultChannel) SendOutput(ctx context.Context, chunk localtaskstore.OutputChunk) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outputs = append(f.outputs, chunk)
	if len(f.outputErrs) > 0 {
		err := f.outputErrs[0]
		f.outputErrs = f.outputErrs[1:]
		return err
	}
	return nil
}

func (f *fakeResultChannel) SendFinal(ctx context.Context, result localtaskstore.FinalResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finals = append(f.finals, result)
	return f.finalErr
}

func waitForOutputs(t *testing.T, channel *fakeResultChannel, count int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		channel.mu.Lock()
		current := len(channel.outputs)
		channel.mu.Unlock()
		if current >= count {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d outputs", count)
}

func outputByStream(outputs []localtaskstore.OutputChunk, stream string) *localtaskstore.OutputChunk {
	for i := range outputs {
		if outputs[i].Stream == stream {
			return &outputs[i]
		}
	}
	return nil
}
