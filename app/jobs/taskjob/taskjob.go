// Package taskjob implements the jobs queue which will poll the data from a
// remote endpoint
package taskjob

import (
	"context"
	"hostlink/db/schema/taskschema"
	"os/exec"
)

func Register() {
	go Trigger(func(tasks []taskschema.Task) error {
		for _, task := range tasks {
			execCmd := exec.Command("/bin/sh", "-c", task.Command)
			output, err := execCmd.CombinedOutput()
			exitCode := 0
			errMsg := ""
			if err != nil {
				if exitError, ok := err.(*exec.ExitError); ok {
					exitCode = exitError.ExitCode()
					task.ExitCode = exitCode
				}
				errMsg = err.Error()
			}
			task.Error = errMsg
			task.Output = string(output)
			task.Status = "completed"
			if err := task.Save(context.Background()); err != nil {
				return err
			}
		}
		return nil
	})
}
