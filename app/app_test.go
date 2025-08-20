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
