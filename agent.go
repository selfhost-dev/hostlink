package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os/exec"
	"time"
)

func startAgent() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		resp, err := http.Get("http://localhost:1323/commands/pending")
		if err != nil {
			continue
		}

		var cmd Command
		if err := json.NewDecoder(resp.Body).Decode(&cmd); err != nil && cmd.ID == "" {
			resp.Body.Close()
			continue
		}
		resp.Body.Close()

		updateCommand(cmd.ID, "running", "", "", 0)

		execCmd := exec.Command("/bin/sh", "-c", cmd.Command)
		output, err := execCmd.CombinedOutput()

		if err != nil {
			exitCode := 0
			if exitError, ok := err.(*exec.ExitError); ok {
				exitCode = exitError.ExitCode()
			}
			updateCommand(cmd.ID, "completed", string(output), err.Error(), exitCode)
		} else {
			updateCommand(cmd.ID, "completed", string(output), "", 0)
		}
	}
}

func updateCommand(id, status, output, errorMsg string, exitCode int) {
	payload, _ := json.Marshal(map[string]any{
		"status":    status,
		"output":    output,
		"error":     errorMsg,
		"exit_code": exitCode,
	})

	req, _ := http.NewRequest("PUT", "http://localhost:1323/commands/"+id, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	http.DefaultClient.Do(req)
}
