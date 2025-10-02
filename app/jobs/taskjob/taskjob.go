// Package taskjob implements the jobs queue which will poll the data from a
// remote endpoint
package taskjob

import (
	"context"
	"fmt"
	"hostlink/domain/task"
	"os"
	"os/exec"
)

func Register(repo task.Repository) {
	go Trigger(func(tasks []task.Task) error {
		for _, t := range tasks {
			tempFile, err := os.CreateTemp("", "*_script.sh")
			if err != nil {
				t.Error = fmt.Sprintf("failed to create temp file: %v", err)
				t.Status = "failed"
				repo.Update(context.Background(), &t)
				continue
			}
			defer os.Remove(tempFile.Name())

			if _, err := tempFile.WriteString("#!/usr/bin/env bash\n" + t.Command); err != nil {
				tempFile.Close()
				t.Error = fmt.Sprintf("failed to write script: %v", err)
				t.Status = "failed"
				repo.Update(context.Background(), &t)
				continue
			}

			if err := os.Chmod(tempFile.Name(), 0755); err != nil {
				t.Error = fmt.Sprintf("failed to chmod: %v", err)
				t.Status = "failed"
				repo.Update(context.Background(), &t)
				continue
			}
			execCmd := exec.Command("/bin/sh", tempFile.Name())
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
			if err := repo.Update(context.Background(), &t); err != nil {
				return err
			}
		}
		return nil
	})
}
