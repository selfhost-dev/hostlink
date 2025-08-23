package app

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSqliteDBCreation(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "anywhere")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.sqlite")
	dbURL := fmt.Sprintf("file:%s", dbPath)

	cfg := NewConfig().WithDBURL(dbURL)

	app := New(cfg)

	err = app.Start()
	if err != nil {
		t.Fatal("Failed to start application", err)
	}
	defer app.Stop()

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Fatal("Database file was not created")
	}
}

func TestSqliteDBPersistence(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "anywhere")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.sqlite")
	dbURL := fmt.Sprintf("file:%s", dbPath)

	cfg := NewConfig().WithDBURL(dbURL)

	app1 := New(cfg)

	err = app1.Start()
	if err != nil {
		t.Fatal("Failed to start application", err)
	}

	_, err = app1.db.Exec(
		`CREATE TABLE test_data (id INTEGER PRIMARY KEY, value TEXT);`,
	)
	if err != nil {
		t.Fatal("Failed to create test table", err)
	}
	_, err = app1.db.Exec(
		`INSERT INTO test_data(value) VALUES ('test_value_123')`,
	)
	if err != nil {
		t.Fatal("Failed to insert data into the test_data", err)
	}

	app1.Stop()

	app2 := New(cfg)
	err = app2.Start()
	if err != nil {
		t.Fatal("Failed to start app2", err)
	}
	defer app2.Stop()

	var value string
	err = app2.db.QueryRow(
		`SELECT value from test_data WHERE value = 'test_value_123'`,
	).Scan(&value)
	if err != nil {
		t.Fatal("Failed to retrieve test data after restart:", err)
	}

	if value != "test_value_123" {
		t.Fatalf(
			"Data was not preserved: expected 'test_value_123', got '%s'", value,
		)
	}

	var tableCount int
	err = app2.db.QueryRow(
		`SELECT COUNT(*) from sqlite_master WHERE type='table' and name='test_data'`,
	).Scan(&tableCount)
	if err != nil {
		t.Fatal("Failed to check table existence:", err)
	}

	if tableCount != 1 {
		t.Fatal("Test table was not preserved after the restart")
	}
}

func TestDatabaseOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "testingdatabaseoperations")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.sqlite")
	dbURL := fmt.Sprintf("file:%s", dbPath)

	cfg := NewConfig().WithDBURL(dbURL)
	app := New(cfg)

	err = app.Start()
	if err != nil {
		t.Fatal("Failed to start the application", err)
	}
	defer app.Stop()

	ctx := context.Background()

	// Test 1: Insert a task
	taskID := "tast-task-123"
	command := "echo 'Hello World'"
	err = app.InsertTask(ctx, taskID, command)
	if err != nil {
		t.Fatal("Failed to insert task:", err)
	}

	// Test 2: Get pipeline task
	gotID, gotCommand, err := app.GetPendingTask(ctx)
	if err != nil {
		t.Fatal("Failed to get pending task:", err)
	}

	if gotID != taskID || gotCommand != command {
		t.Fatalf(
			"Task mismatch: expected ID=%s, command=%s, got ID=%s, command=%s",
			taskID, command, gotID, gotCommand,
		)
	}

	err = app.UpdateTaskStatus(ctx, taskID, "running", "", "", 0)
	if err != nil {
		t.Fatal("Failed to update task status:", err)
	}

	// Verify no pending task after the update
	_, _, err = app.GetPendingTask(ctx)
	if err != sql.ErrNoRows {
		t.Fatalf("Expected sql.ErrNoRows when no pending tasks, but got: %v", err)
	}

	err = app.UpdateTaskStatus(ctx, taskID, "completed", "Hello World", "", 0)
	if err != nil {
		t.Fatal("Failed to update task completed:", err)
	}
}

func TestTaskPersistenceAcrossRestarts(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "testtaskpersistenceacrossrestarts")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "test.sqlite")
	dbURL := fmt.Sprintf("file:%s", dbPath)

	cfg := NewConfig().WithDBURL(dbURL)
	app1 := New(cfg)

	err = app1.Start()
	if err != nil {
		t.Fatal("Failed to start the app1", err)
	}

	ctx := context.Background()
	tasks := []struct {
		id      string
		command string
	}{
		{"task-1", "ls -al"},
		{"task-2", "pwd"},
		{"task-3", "echo test"},
	}

	for _, task := range tasks {
		err := app1.InsertTask(ctx, task.id, task.command)
		if err != nil {
			t.Fatalf("Failed to insert task %s: %v", task.id, err)
		}
	}

	err = app1.UpdateTaskStatus(ctx, "task-1", "completed", "output", "", 0)
	if err != nil {
		t.Fatal("Failed to update task-1:", err)
	}

	app1.Stop()

	// second app check the data persists
	app2 := New(cfg)
	err = app2.Start()
	if err != nil {
		t.Fatal("Failed to start app2", err)
	}
	defer app2.Stop()

	// Should get task-2 and the next pending task
	id, command, err := app2.GetPendingTask(ctx)
	if err != nil {
		t.Fatal("Failed to get pending task after restart:", err)
	}
	if id != "task-2" || command != "pwd" {
		t.Fatalf("Expected task-2 with command 'pwd', got id=%s, command=%s", id, command)
	}
}
