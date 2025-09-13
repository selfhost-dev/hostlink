// Package agent deals with all the logic when hostlink is run in the agent mode
package agent

import (
	"context"
	"hostlink/app"
	"os/exec"
	"time"
)

type (
	TaskFetcher func(context.Context) (*app.Task, error)
	TaskUpdater func(context.Context, app.Task) error
)

type Agent struct {
	taskFetchFn  TaskFetcher
	taskUpdateFn TaskUpdater
}

func New(taskFetchFn TaskFetcher, taskUpdateFn TaskUpdater) *Agent {
	return &Agent{
		taskFetchFn:  taskFetchFn,
		taskUpdateFn: taskUpdateFn,
	}
}

func (a Agent) StartAgent() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		task, err := a.taskFetchFn(context.Background())
		if err != nil {
			continue
		}

		a.taskUpdateFn(context.Background(), app.Task{
			PID:    task.PID,
			Status: "running",
		})

		execCmd := exec.Command("/bin/sh", "-c", task.Command)
		output, err := execCmd.CombinedOutput()
		exitCode := 0
		errMsg := ""
		if err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			}
			errMsg = err.Error()
		}
		a.taskUpdateFn(context.Background(), app.Task{
			PID:      task.PID,
			Status:   "completed",
			Output:   string(output),
			Error:    errMsg,
			ExitCode: exitCode,
		})
	}
}
