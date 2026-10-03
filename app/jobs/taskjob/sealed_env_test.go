package taskjob

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"hostlink/domain/task"
	"hostlink/internal/crypto"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// selfhost #3168: secrets reach a task's script through its environment, opened from the task's
// sealed env, never through the command the control plane stores.
func TestTaskJobRunsTheScriptWithItsSealedEnv(t *testing.T) {
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{
		Trigger: runOnceTrigger,
		SealedEnvOpener: func(sealed, taskID string) (map[string]string, error) {
			if sealed != "envelope" || taskID != "task-1" {
				t.Fatalf("opened %q for %q", sealed, taskID)
			}
			return map[string]string{"TLS_KEY_PEM": "s3cret"}, nil
		},
	})

	job.processTask(context.Background(), task.Task{ID: "task-1", Command: `printf '%s' "$TLS_KEY_PEM"`, SealedEnv: "envelope"}, reporter, nil)

	if got := reporter.results[0]; got.Status != "completed" || got.ExitCode != 0 || got.Output != "s3cret" {
		t.Fatalf("result = %+v", got)
	}
}

func TestTaskJobFailsATaskWhoseSealedEnvDoesNotOpen(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{
		Trigger:         runOnceTrigger,
		SealedEnvOpener: func(string, string) (map[string]string, error) { return nil, errors.New("wrong agent key") },
	})

	job.processTask(context.Background(), task.Task{ID: "task-1", Command: "touch " + marker, SealedEnv: "envelope"}, reporter, nil)

	got := reporter.results[0]
	if got.Status != "failed" || !strings.Contains(got.Error, "sealed env") || !strings.Contains(got.Error, "wrong agent key") {
		t.Fatalf("result = %+v", got)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the script ran without its sealed env")
	}
}

func TestTaskJobLeavesTheEnvironmentAloneWithoutASealedEnv(t *testing.T) {
	reporter := &fakeTaskReporter{}
	job := NewJobWithConf(TaskJobConfig{
		Trigger:         runOnceTrigger,
		SealedEnvOpener: func(string, string) (map[string]string, error) { t.Fatal("opened a missing envelope"); return nil, nil },
	})

	job.processTask(context.Background(), task.Task{ID: "task-1", Command: "printf ok"}, reporter, nil)

	if got := reporter.results[0]; got.Status != "completed" || got.Output != "ok" {
		t.Fatalf("result = %+v", got)
	}
}

// The default opener reads the agent's own private key, as registration stored it.
func TestDefaultSealedEnvOpenerUsesTheAgentKey(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "agent.key")
	if err := crypto.SavePrivateKey(key, keyPath); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOSTLINK_PRIVATE_KEY_PATH", keyPath)
	sealed, err := crypto.SealEnv(map[string]string{"A": "1"}, "task-1", &key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}

	env, err := defaultSealedEnvOpener(sealed, "task-1")

	if err != nil || env["A"] != "1" {
		t.Fatalf("env = %v, err = %v", env, err)
	}
}
