package upgrade

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	// DefaultLogPath is the default path for upgrade logs.
	DefaultLogPath = "/var/log/hostlink/upgrade.log"
)

// NewLogger creates a structured JSON logger that writes to both the given
// log file (append mode) and stderr. Returns the logger and a cleanup function
// that closes the log file.
func NewLogger(logPath string) (*slog.Logger, func(), error) {
	// Ensure log directory exists
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, nil, err
	}

	// Open log file in append mode
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, err
	}

	// Multi-writer: file + stderr
	w := io.MultiWriter(f, os.Stderr)

	handler := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})

	logger := slog.New(handler)

	cleanup := func() {
		f.Close()
	}

	return logger, cleanup, nil
}

// discardLogger returns a logger that writes nothing (for testing).
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}
