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
	task := Task{
		PID:      "tast-task-123",
		Command:  "echo 'Hello World'",
		Priority: 0,
	}
	err = app.InsertTask(ctx, task)
	if err != nil {
		t.Fatal("Failed to insert task:", err)
	}

	// Test 2: Get pipeline task
	newTask, err := app.GetPendingTask(ctx)
	if err != nil {
		t.Fatal("Failed to get pending task:", err)
	}

	if newTask.PID != task.PID || newTask.Command != task.Command {
		t.Fatalf(
			"Task mismatch: expected PID=%s, command=%s, got PID=%s, command=%s",
			task.PID, task.Command, newTask.PID, newTask.Command,
		)
	}

	err = app.UpdateTask(ctx, Task{
		PID:      task.PID,
		Status:   "running",
		Priority: 0,
	})
	if err != nil {
		t.Fatal("Failed to update task status:", err)
	}

	// Verify no pending task after the update
	_, err = app.GetPendingTask(ctx)
	if err != sql.ErrNoRows {
		t.Fatalf("Expected sql.ErrNoRows when no pending tasks, but got: %v", err)
	}

	err = app.UpdateTask(ctx, Task{
		PID:      task.PID,
		Status:   "completed",
		Output:   "Hello World",
		Priority: 0,
	})
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
	tasks := []Task{
		{PID: "task-1", Command: "ls -al"},
		{PID: "task-2", Command: "pwd"},
		{PID: "task-3", Command: "echo test"},
	}

	for _, task := range tasks {
		err := app1.InsertTask(ctx, task)
		if err != nil {
			t.Fatalf("Failed to insert task %s: %v", task.PID, err)
		}
	}

	err = app1.UpdateTask(ctx, Task{
		PID:      "task-1",
		Status:   "completed",
		Output:   "output",
		Priority: 0,
	})
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
	task, err := app2.GetPendingTask(ctx)
	if err != nil {
		t.Fatal("Failed to get pending task after restart:", err)
	}
	if task.PID != "task-2" || task.Command != "pwd" {
		t.Fatalf(
			"Expected task-2 with command 'pwd', got id=%s, command=%s",
			task.PID,
			task.Command,
		)
	}
}

func TestGetAllTasks(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "testgetalltasks")
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
		t.Fatal("Failed to start the app1", err)
	}
	defer app.Stop()

	ctx := context.Background()

	// Insert multiple tasks with different statuses
	tasks := []Task{
		{PID: "task-1", Command: "echo 'first'", Priority: 0, Status: "pending"},
		{PID: "task-2", Command: "echo 'second'", Priority: 5, Status: "running"},
		{PID: "task-3", Command: "echo 'thrird'", Priority: 0, Status: "completed"},
		{PID: "task-4", Command: "echo 'fourth'", Priority: 10, Status: "pending"},
	}

	for _, task := range tasks {
		err := app.InsertTask(ctx, task)
		if err != nil {
			t.Fatalf("Failed to insert task %s: %v", task.PID, err)
		}
	}

	err = app.UpdateTask(ctx, Task{
		PID:      "task-2",
		Status:   "running",
		Output:   "output",
		Priority: 0,
	})
	if err != nil {
		t.Fatal("Failed to update task-2 to running:", err)
	}

	err = app.UpdateTask(ctx, Task{
		PID:      "task-3",
		Status:   "completed",
		Output:   "output",
		Priority: 0,
	})
	if err != nil {
		t.Fatal("Failed to update task-3 to completed:", err)
	}

	allTasks, err := app.GetAllTasks(ctx)
	if err != nil {
		t.Fatal("Failed to get all tasks:", err)
	}

	if len(allTasks) != len(tasks) {
		t.Fatalf("Expected %d tasks, got %d", len(tasks), len(allTasks))
	}

	taskMap := make(map[string]Task)
	for _, task := range allTasks {
		taskMap[task.PID] = task
	}

	if taskMap["task-2"].Priority != 5 {
		t.Fatalf("Expected task-2 priority 5, got %d", taskMap["task-2"].Priority)
	}

	if taskMap["task-4"].Priority != 10 {
		t.Fatalf("Expected task-4 priority 10, got %d", taskMap["task-4"].Priority)
	}

	expectedStatuses := map[string]string{
		"task-1": "pending",
		"task-2": "running",
		"task-3": "completed",
		"task-4": "pending",
	}

	for taskID, expectedStatus := range expectedStatuses {
		if taskMap[taskID].Status != expectedStatus {
			t.Fatalf("Task %s: expected status %s, got %s", taskID, expectedStatus, taskMap[taskID].Status)
		}
	}

	if taskMap["task-3"].Output != "output" {
		t.Fatalf("Task-3 should have output 'output', got '%s'", taskMap["task-3"].Output)
	}

	if allTasks[0].PID != "task-4" {
		t.Fatalf("Expected first task to be task-4 (newest), got %s", allTasks[0].PID)
	}
}

func TestTaskPriority(t *testing.T) {
	tempDir, err := os.MkdirTemp("/tmp", "testtaskpriority")
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
		t.Fatal("Failed to start the app1", err)
	}
	defer app.Stop()

	ctx := context.Background()

	// Insert multiple tasks with different statuses
	tasks := []Task{
		{PID: "low-priority", Command: "echo 'low'", Priority: 0},
		{PID: "high-priority", Command: "echo 'high'", Priority: 5},
		{PID: "medium-priority", Command: "echo 'medium'", Priority: 0},
		{PID: "urgent", Command: "echo 'urgent'", Priority: 10},
	}

	for _, task := range tasks {
		err := app.InsertTask(ctx, task)
		if err != nil {
			t.Fatalf("Failed to insert task %s: %v", task.PID, err)
		}
	}
	task, err := app.GetPendingTask(ctx)
	if err != nil {
		t.Fatal("Failed to get pending task:", err)
	}
	if task.PID != "urgent" {
		t.Fatalf("Expected 'urgent' task first, got %s", task.PID)
	}

	err = app.UpdateTask(ctx, Task{
		PID:      "urgent",
		Status:   "completed",
		Priority: 0,
	})
	if err != nil {
		t.Fatal("Failed to update the urgent task:", err)
	}
	task, err = app.GetPendingTask(ctx)
	if err != nil {
		t.Fatal("Failed to get pending task:", err)
	}
	if task.PID != "high-priority" {
		t.Fatalf("Expected 'high-priority' task, got %s", task.PID)
	}
}
