// Package taskjob implements the jobs queue which will poll the data from a
// remote endpoint
package taskjob

import (
	"context"
	"fmt"
	"hostlink/app/services/taskfetcher"
	"hostlink/app/services/taskreporter"
	"hostlink/domain/task"
	"os"
	"os/exec"
	"sync"

	"github.com/labstack/gommon/log"
)

type TriggerFunc func(context.Context, func() error)

type TaskJobConfig struct {
	Trigger TriggerFunc
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

	return &TaskJob{
		config: cfg,
	}
}

func (tj *TaskJob) Register(ctx context.Context, tf taskfetcher.TaskFetcher, tr taskreporter.TaskReporter) context.CancelFunc {
	ctx, cancel := context.WithCancel(ctx)
	tj.cancel = cancel
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
				tj.processTask(t, tr)
			}
			return nil
		})
	}()
	return cancel
}

func (tj *TaskJob) processTask(t task.Task, tr taskreporter.TaskReporter) {
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

	if _, err := tempFile.WriteString("#!/usr/bin/env bash\n" + t.Command); err != nil {
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
	execCmd := exec.Command("/bin/bash", tempFile.Name())
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

func (tj *TaskJob) Shutdown() {
	if tj.cancel != nil {
		tj.cancel()
	}
	tj.wg.Wait()
}
