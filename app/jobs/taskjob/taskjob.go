// Package taskjob implements the jobs queue which will poll the data from a
// remote endpoint
package taskjob

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hostlink/app/services/localtaskstore"
	"hostlink/app/services/taskfetcher"
	"hostlink/app/services/taskreporter"
	"hostlink/domain/task"
	"hostlink/internal/telemetry"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/labstack/gommon/log"
)

type TriggerFunc func(context.Context, func() error)

type PollingGate interface {
	ShouldPoll() bool
}

type TaskJobConfig struct {
	Trigger              TriggerFunc
	OutputFlushInterval  time.Duration
	OutputFlushThreshold int
	PollingGate          PollingGate
}

type ResultChannel interface {
	SendStarted(context.Context, localtaskstore.TaskReceipt) error
	SendOutput(context.Context, localtaskstore.OutputChunk) error
	SendFinal(context.Context, localtaskstore.FinalResult) error
}

type TaskJob struct {
	config     TaskJobConfig
	enqueueCh  chan task.Task
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	inFlightMu sync.Mutex
	inFlight   map[string]struct{}
}

func New() *TaskJob {
	return NewJobWithConf(TaskJobConfig{
		Trigger: Trigger,
	})
}

func NewJobWithConf(cfg TaskJobConfig) *TaskJob {
	if cfg.Trigger == nil {
		cfg.Trigger = Trigger
	}
	if cfg.OutputFlushInterval == 0 {
		cfg.OutputFlushInterval = 100 * time.Millisecond
	}
	if cfg.OutputFlushThreshold == 0 {
		cfg.OutputFlushThreshold = 16 * 1024
	}

	return &TaskJob{
		config:    cfg,
		enqueueCh: make(chan task.Task, 16),
		inFlight:  make(map[string]struct{}),
	}
}

// acquireTask reserves a task ID for execution. It returns false if the
// task is already queued or running. Reservation happens at Enqueue time
// (not just when execution actually starts) so that repeated calls from any
// source — WebSocket push, HTTP polling, or heartbeat responses — can't pile
// up duplicate, not-yet-started copies of the same task in the queue.
func (tj *TaskJob) acquireTask(taskID string) bool {
	tj.inFlightMu.Lock()
	defer tj.inFlightMu.Unlock()
	if _, running := tj.inFlight[taskID]; running {
		return false
	}
	tj.inFlight[taskID] = struct{}{}
	return true
}

func (tj *TaskJob) releaseTask(taskID string) {
	tj.inFlightMu.Lock()
	defer tj.inFlightMu.Unlock()
	delete(tj.inFlight, taskID)
}

func (tj *TaskJob) Register(ctx context.Context, tf taskfetcher.TaskFetcher, tr taskreporter.TaskReporter, channels ...ResultChannel) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	tj.cancel = cancel
	var channel ResultChannel
	if len(channels) > 0 {
		channel = channels[0]
	}
	tj.wg.Add(1)
	go func() {
		defer tj.wg.Done()
		for {
			select {
			case queued := <-tj.enqueueCh:
				tj.processTaskSafe(ctx, queued, tr, channel)
			case <-ctx.Done():
				return
			}
		}
	}()
	tj.wg.Add(1)
	go func() {
		defer tj.wg.Done()
		tj.config.Trigger(ctx, func() error {
			if tj.config.PollingGate != nil && !tj.config.PollingGate.ShouldPoll() {
				return nil
			}
			allTasks, err := tf.Fetch()
			if err != nil {
				return err
			}
			incompleteTasks := []task.Task{}
			for _, task := range allTasks {
				if task.Status != "completed" {
					incompleteTasks = append(incompleteTasks, task)
				}
			}
			for _, t := range incompleteTasks {
				if err := tj.Enqueue(ctx, t); err != nil {
					log.Errorf("failed to enqueue polled task %s: %v", t.ID, err)
				}
			}
			return nil
		})
	}()
	return cancel
}

// Enqueue queues a task for execution. It is the single dedup choke point
// for all three task sources (WebSocket push, HTTP polling, heartbeat
// responses): a task ID already queued or currently running is skipped
// silently rather than queued again. Without this, a task that hasn't yet
// reported "started" back to the control plane (e.g. anything delivered
// without an execution_attempt_id) keeps looking "pending" to every caller
// that re-checks it — heartbeat ticks every 5s — and each one would queue
// another duplicate run of the same (possibly destructive) command.
func (tj *TaskJob) Enqueue(ctx context.Context, t task.Task) error {
	if !tj.acquireTask(t.ID) {
		return nil
	}
	select {
	case tj.enqueueCh <- t:
		telemetry.Metric("hostlink.task_runner.queue.depth", len(tj.enqueueCh), map[string]any{
			"task_id":              t.ID,
			"execution_attempt_id": t.ExecutionAttemptID,
		})
		return nil
	case <-ctx.Done():
		tj.releaseTask(t.ID)
		return ctx.Err()
	}
}

func (tj *TaskJob) processTaskSafe(ctx context.Context, t task.Task, tr taskreporter.TaskReporter, channel ResultChannel) {
	defer func() {
		if r := recover(); r != nil {
			log.Errorf("Panic recovered in processTask: %v", r)
			telemetry.Metric("hostlink.task_runner.panic", 1, map[string]any{
				"task_id": t.ID,
			})
		}
	}()
	tj.processTask(ctx, t, tr, channel)
}

// processTask runs a task's command. Callers reaching this via Register()
// (the enqueueCh consumer) have already had the task ID reserved by
// Enqueue; this only owns releasing that reservation once execution
// finishes. Direct callers (e.g. tests) that bypass Enqueue don't hold a
// reservation, so the release below is a harmless no-op for them.
func (tj *TaskJob) processTask(ctx context.Context, t task.Task, tr taskreporter.TaskReporter, channel ResultChannel) {
	defer tj.releaseTask(t.ID)

	tempFile, err := os.CreateTemp("", "*_script.sh")
	if err != nil {
		t.Error = fmt.Sprintf("failed to create temp file: %v", err)
		t.Status = "failed"
		if reportErr := tr.Report(t.ID, &taskreporter.TaskResult{
			Status:   t.Status,
			Output:   t.Output,
			Error:    t.Error,
			ExitCode: t.ExitCode,
		}); reportErr != nil {
			log.Errorf("failed to report task %s: %v", t.ID, reportErr)
		}
		return
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.WriteString(t.Command); err != nil {
		tempFile.Close()
		t.Error = fmt.Sprintf("failed to write script: %v", err)
		t.Status = "failed"
		if reportErr := tr.Report(t.ID, &taskreporter.TaskResult{
			Status:   t.Status,
			Output:   t.Output,
			Error:    t.Error,
			ExitCode: t.ExitCode,
		}); reportErr != nil {
			log.Errorf("failed to report task %s: %v", t.ID, reportErr)
		}
		return
	}
	tempFile.Close()

	if err := os.Chmod(tempFile.Name(), 0755); err != nil {
		t.Error = fmt.Sprintf("failed to chmod: %v", err)
		t.Status = "failed"
		if reportErr := tr.Report(t.ID, &taskreporter.TaskResult{
			Status:   t.Status,
			Output:   t.Output,
			Error:    t.Error,
			ExitCode: t.ExitCode,
		}); reportErr != nil {
			log.Errorf("failed to report task %s: %v", t.ID, reportErr)
		}
		return
	}
	execCmd := exec.Command("/bin/sh", "-c", tempFile.Name())
	if channel != nil && t.ExecutionAttemptID != "" {
		tj.processTaskWithResultChannel(ctx, t, execCmd, tr, channel)
		return
	}

	output, err := execCmd.CombinedOutput()
	exitCode := 0
	errMsg := ""
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
			t.ExitCode = exitCode
		}
		errMsg = err.Error()
	}
	t.Error = errMsg
	t.Output = string(output)
	t.Status = "completed"
	if reportErr := tr.Report(t.ID, &taskreporter.TaskResult{
		Status:   t.Status,
		Output:   t.Output,
		Error:    t.Error,
		ExitCode: t.ExitCode,
	}); reportErr != nil {
		log.Errorf("failed to report task %s: %v", t.ID, reportErr)
	}
}

func (tj *TaskJob) processTaskWithResultChannel(ctx context.Context, t task.Task, execCmd *exec.Cmd, tr taskreporter.TaskReporter, channel ResultChannel) {
	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		tj.reportHTTPResult(t, tr, "failed", "", fmt.Sprintf("failed to capture stdout: %v", err), 1)
		return
	}
	stderr, err := execCmd.StderrPipe()
	if err != nil {
		tj.reportHTTPResult(t, tr, "failed", "", fmt.Sprintf("failed to capture stderr: %v", err), 1)
		return
	}

	if err := execCmd.Start(); err != nil {
		tj.reportHTTPResult(t, tr, "failed", "", err.Error(), 1)
		return
	}
	if err := channel.SendStarted(ctx, localtaskstore.TaskReceipt{TaskID: t.ID, ExecutionAttemptID: t.ExecutionAttemptID}); err != nil {
		_ = execCmd.Process.Kill()
		tj.reportHTTPResult(t, tr, "failed", "", fmt.Sprintf("failed to report task start: %v", err), 1)
		return
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		tj.captureStream(ctx, t, "stdout", stdout, &stdoutBuf, channel)
	}()
	go func() {
		defer wg.Done()
		tj.captureStream(ctx, t, "stderr", stderr, &stderrBuf, channel)
	}()
	wg.Wait()

	exitCode := 0
	status := "completed"
	errMsg := ""
	if err := execCmd.Wait(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = 1
		}
		status = "failed"
		errMsg = err.Error()
	}

	if stderrBuf.Len() > 0 {
		errMsg = stderrBuf.String()
	}
	output := stdoutBuf.String()
	resultPayload := taskreporter.TaskResult{Status: status, Output: output, Error: errMsg, ExitCode: exitCode}
	finalPayload, err := json.Marshal(resultPayload)
	if err != nil {
		tj.reportHTTPResult(t, tr, status, output, errMsg, exitCode)
		return
	}

	final := localtaskstore.FinalResult{
		MessageID:          messageID(t.ID, t.ExecutionAttemptID, "final", 0),
		TaskID:             t.ID,
		ExecutionAttemptID: t.ExecutionAttemptID,
		Status:             status,
		ExitCode:           exitCode,
		Payload:            string(finalPayload),
	}
	if err := channel.SendFinal(ctx, final); err != nil {
		tj.reportHTTPResult(t, tr, status, output, errMsg, exitCode)
	}
}

func (tj *TaskJob) captureStream(ctx context.Context, t task.Task, stream string, reader io.Reader, sink *bytes.Buffer, channel ResultChannel) {
	sequence := int64(1)
	chunks := make(chan string, 1)
	go func() {
		defer close(chunks)
		buffered := bufio.NewReaderSize(reader, tj.config.OutputFlushThreshold)
		for {
			buf := make([]byte, max(tj.config.OutputFlushThreshold, 1))
			n, err := buffered.Read(buf)
			if n > 0 {
				chunks <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	var pending bytes.Buffer
	ticker := time.NewTicker(tj.config.OutputFlushInterval)
	defer ticker.Stop()

	flush := func() bool {
		if pending.Len() == 0 {
			return true
		}
		chunk := pending.String()
		err := channel.SendOutput(ctx, localtaskstore.OutputChunk{
			MessageID:          messageID(t.ID, t.ExecutionAttemptID, stream, sequence),
			TaskID:             t.ID,
			ExecutionAttemptID: t.ExecutionAttemptID,
			Stream:             stream,
			Sequence:           sequence,
			Payload:            chunk,
			ByteCount:          int64(len(chunk)),
		})
		if err != nil {
			return false
		}
		pending.Reset()
		sequence++
		return true
	}

	for {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				flush()
				return
			}
			sink.WriteString(chunk)
			pending.WriteString(chunk)
			if pending.Len() >= tj.config.OutputFlushThreshold {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			return
		}
	}
}

func (tj *TaskJob) reportHTTPResult(t task.Task, tr taskreporter.TaskReporter, status, output, errMsg string, exitCode int) {
	if reportErr := tr.Report(t.ID, &taskreporter.TaskResult{
		Status:   status,
		Output:   output,
		Error:    errMsg,
		ExitCode: exitCode,
	}); reportErr != nil {
		log.Errorf("failed to report task %s: %v", t.ID, reportErr)
	}
}

func messageID(taskID, attemptID, stream string, sequence int64) string {
	parts := []string{"msg", taskID, attemptID, stream, fmt.Sprintf("%d", sequence), fmt.Sprintf("%d", time.Now().UnixNano())}
	return strings.NewReplacer("/", "-", " ", "-", "|", "-").Replace(strings.Join(parts, "-"))
}

func (tj *TaskJob) Shutdown() {
	if tj.cancel != nil {
		tj.cancel()
	}
	tj.wg.Wait()
}
