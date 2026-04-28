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
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/labstack/gommon/log"
)

type TriggerFunc func(context.Context, func() error)

type TaskJobConfig struct {
	Trigger              TriggerFunc
	OutputFlushInterval  time.Duration
	OutputFlushThreshold int
}

type ResultChannel interface {
	RecordStarted(context.Context, localtaskstore.TaskReceipt) error
	SendStarted(context.Context, localtaskstore.TaskReceipt) error
	SendOutput(context.Context, localtaskstore.OutputChunk) error
	SendFinal(context.Context, localtaskstore.FinalResult) error
}

type TaskExecutor interface {
	Execute(context.Context, task.Task) error
}

type taskExecutor struct {
	config   TaskJobConfig
	reporter taskreporter.TaskReporter
	channel  ResultChannel
}

type TaskJob struct {
	config       TaskJobConfig
	enqueueCh    chan task.Task
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	mu           sync.Mutex
	knownAttempt map[string]struct{}
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
		config:       cfg,
		enqueueCh:    make(chan task.Task, 16),
		knownAttempt: make(map[string]struct{}),
	}
}

func (tj *TaskJob) Register(ctx context.Context, tf taskfetcher.TaskFetcher, tr taskreporter.TaskReporter, channels ...ResultChannel) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	tj.cancel = cancel
	var channel ResultChannel
	if len(channels) > 0 {
		channel = channels[0]
	}
	executor := &taskExecutor{config: tj.config, reporter: tr, channel: channel}
	tj.wg.Add(1)
	go func() {
		defer tj.wg.Done()
		tj.run(ctx, executor)
	}()
	tj.wg.Add(1)
	go func() {
		defer tj.wg.Done()
		tj.config.Trigger(ctx, func() error {
			allTasks, err := tf.Fetch()
			if err != nil {
				return err
			}
			for _, t := range allTasks {
				if t.Status == "completed" {
					continue
				}
				if err := tj.Enqueue(ctx, t); err != nil {
					return err
				}
			}
			return nil
		})
	}()
	return cancel
}

func (tj *TaskJob) run(ctx context.Context, executor TaskExecutor) {
	for {
		select {
		case queued := <-tj.enqueueCh:
			executor.Execute(ctx, queued)
			tj.finishAttempt(queued)
		case <-ctx.Done():
			return
		}
	}
}

func (tj *TaskJob) Enqueue(ctx context.Context, t task.Task) error {
	if !tj.reserveAttempt(t) {
		return nil
	}
	select {
	case tj.enqueueCh <- t:
		return nil
	case <-ctx.Done():
		tj.finishAttempt(t)
		return ctx.Err()
	}
}

func (tj *TaskJob) reserveAttempt(t task.Task) bool {
	key := executionKey(t)
	if key == "" {
		return true
	}
	tj.mu.Lock()
	defer tj.mu.Unlock()
	if _, ok := tj.knownAttempt[key]; ok {
		return false
	}
	tj.knownAttempt[key] = struct{}{}
	return true
}

func (tj *TaskJob) finishAttempt(t task.Task) {
	key := executionKey(t)
	if key == "" {
		return
	}
	tj.mu.Lock()
	defer tj.mu.Unlock()
	delete(tj.knownAttempt, key)
}

func executionKey(t task.Task) string {
	if t.ID == "" || t.ExecutionAttemptID == "" {
		return ""
	}
	return t.ID + "\x00" + t.ExecutionAttemptID
}

func (e *taskExecutor) Execute(ctx context.Context, t task.Task) error {
	e.processTask(ctx, t)
	return nil
}

func (tj *TaskJob) processTask(ctx context.Context, t task.Task, tr taskreporter.TaskReporter, channel ResultChannel) {
	executor := &taskExecutor{config: tj.config, reporter: tr, channel: channel}
	executor.processTask(ctx, t)
}

func (tj *TaskJob) captureStream(ctx context.Context, t task.Task, stream string, reader io.Reader, sink *bytes.Buffer, channel ResultChannel) {
	executor := &taskExecutor{config: tj.config, channel: channel}
	executor.captureStream(ctx, t, stream, reader, sink)
}

func (e *taskExecutor) processTask(ctx context.Context, t task.Task) {
	tempFile, err := os.CreateTemp("", "*_script.sh")
	if err != nil {
		t.Error = fmt.Sprintf("failed to create temp file: %v", err)
		t.Status = "failed"
		e.reportHTTPResult(t, "failed", t.Output, t.Error, t.ExitCode)
		return
	}
	defer os.Remove(tempFile.Name())

	if _, err := tempFile.WriteString(t.Command); err != nil {
		tempFile.Close()
		t.Error = fmt.Sprintf("failed to write script: %v", err)
		t.Status = "failed"
		e.reportHTTPResult(t, "failed", t.Output, t.Error, t.ExitCode)
		return
	}
	tempFile.Close()

	if err := os.Chmod(tempFile.Name(), 0755); err != nil {
		t.Error = fmt.Sprintf("failed to chmod: %v", err)
		t.Status = "failed"
		e.reportHTTPResult(t, "failed", t.Output, t.Error, t.ExitCode)
		return
	}
	execCmd := exec.Command("/bin/sh", "-c", tempFile.Name())
	if e.channel != nil && t.ExecutionAttemptID != "" {
		e.processTaskWithResultChannel(ctx, t, execCmd)
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
	e.reportHTTPResult(t, t.Status, t.Output, t.Error, t.ExitCode)
}

func (e *taskExecutor) processTaskWithResultChannel(ctx context.Context, t task.Task, execCmd *exec.Cmd) {
	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		e.reportHTTPResult(t, "failed", "", fmt.Sprintf("failed to capture stdout: %v", err), 1)
		return
	}
	stderr, err := execCmd.StderrPipe()
	if err != nil {
		e.reportHTTPResult(t, "failed", "", fmt.Sprintf("failed to capture stderr: %v", err), 1)
		return
	}

	receipt := localtaskstore.TaskReceipt{TaskID: t.ID, ExecutionAttemptID: t.ExecutionAttemptID}
	if err := e.channel.RecordStarted(ctx, receipt); err != nil {
		e.reportHTTPResult(t, "failed", "", fmt.Sprintf("failed to record task start: %v", err), 1)
		return
	}
	if err := execCmd.Start(); err != nil {
		e.reportHTTPResult(t, "failed", "", err.Error(), 1)
		return
	}
	if err := e.channel.SendStarted(ctx, receipt); err != nil {
		_ = execCmd.Process.Kill()
		e.reportHTTPResult(t, "failed", "", fmt.Sprintf("failed to report task start: %v", err), 1)
		return
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		e.captureStream(ctx, t, "stdout", stdout, &stdoutBuf)
	}()
	go func() {
		defer wg.Done()
		e.captureStream(ctx, t, "stderr", stderr, &stderrBuf)
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
		e.reportHTTPResult(t, status, output, errMsg, exitCode)
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
	if err := e.channel.SendFinal(ctx, final); err != nil {
		e.reportHTTPResult(t, status, output, errMsg, exitCode)
	}
}

func (e *taskExecutor) captureStream(ctx context.Context, t task.Task, stream string, reader io.Reader, sink *bytes.Buffer) {
	sequence := int64(1)
	chunks := make(chan string, 1)
	go func() {
		defer close(chunks)
		buffered := bufio.NewReaderSize(reader, e.config.OutputFlushThreshold)
		for {
			buf := make([]byte, max(e.config.OutputFlushThreshold, 1))
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
	ticker := time.NewTicker(e.config.OutputFlushInterval)
	defer ticker.Stop()

	flush := func() bool {
		if pending.Len() == 0 {
			return true
		}
		chunk := pending.String()
		err := e.channel.SendOutput(ctx, localtaskstore.OutputChunk{
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
			if pending.Len() >= e.config.OutputFlushThreshold {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			return
		}
	}
}

func (e *taskExecutor) reportHTTPResult(t task.Task, status, output, errMsg string, exitCode int) {
	if reportErr := e.reporter.Report(t.ID, &taskreporter.TaskResult{
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
