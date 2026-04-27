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
	SendOutput(context.Context, localtaskstore.OutputChunk) error
	SendFinal(context.Context, localtaskstore.FinalResult) error
}

type TaskJob struct {
	config TaskJobConfig
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
		config: cfg,
	}
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
		tj.config.Trigger(ctx, func() error {
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
				tj.processTask(ctx, t, tr, channel)
			}
			return nil
		})
	}()
	return cancel
}

func (tj *TaskJob) processTask(ctx context.Context, t task.Task, tr taskreporter.TaskReporter, channel ResultChannel) {
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
