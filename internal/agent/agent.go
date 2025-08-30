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

func StartAgent(tf TaskFetcher, tu TaskUpdater) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		task, err := tf(context.Background())
		if err != nil {
			continue
		}

		tu(context.Background(), app.Task{
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
		tu(context.Background(), app.Task{
			PID:      task.PID,
			Status:   "completed",
			Output:   string(output),
			Error:    errMsg,
			ExitCode: exitCode,
		})
	}
}
