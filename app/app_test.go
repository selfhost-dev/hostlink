package app

import (
	"fmt"
	"hostlink/config"
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

	cfg := config.New().WithDBURL(dbURL)

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

	cfg := config.New().WithDBURL(dbURL)

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
