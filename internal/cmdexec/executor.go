package cmdexec

import (
	"context"
	"os/exec"
)

type Executor struct{}

func New() *Executor {
	return &Executor{}
}

func (e *Executor) Execute(ctx context.Context, command string) (string, error) {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), err
	}
	return string(output), nil
}
