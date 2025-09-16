// Package taskjob implements the jobs queue which will poll the data from a
// remote endpoint
package taskjob

import (
	"context"
	"fmt"
	"hostlink/db/schema/taskschema"
	"os"
	"os/exec"
)

func Register() {
	go Trigger(func(tasks []taskschema.Task) error {
		for _, task := range tasks {
			tempFile, err := os.CreateTemp("", "*_script.sh")
			if err != nil {
				task.Error = fmt.Sprintf("failed to create temp file: %v", err)
				task.Status = "failed"
				task.Save(context.Background())
				continue
			}
			defer os.Remove(tempFile.Name())

			if _, err := tempFile.WriteString("#!/usr/bin/env bash\n" + task.Command); err != nil {
				tempFile.Close()
				task.Error = fmt.Sprintf("failed to write script: %v", err)
				task.Status = "failed"
				task.Save(context.Background())
				continue
			}

			if err := os.Chmod(tempFile.Name(), 0755); err != nil {
				task.Error = fmt.Sprintf("failed to chmod: %v", err)
				task.Status = "failed"
				task.Save(context.Background())
				continue
			}
			execCmd := exec.Command("/bin/sh", tempFile.Name())
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
