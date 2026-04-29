package telemetrytest

import (
	"encoding/json"
	"os"
	"testing"
)

func ReadEntries(t *testing.T, path string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read telemetry file: %v", err)
	}
	lines := splitNonEmptyLines(data)
	entries := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("unmarshal telemetry line %q: %v", string(line), err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func FindEntry(entries []map[string]any, match func(map[string]any) bool) map[string]any {
	for _, entry := range entries {
		if match(entry) {
			return entry
		}
	}
	return map[string]any{}
}

func splitNonEmptyLines(data []byte) [][]byte {
	lines := [][]byte{}
	start := 0
	for i, b := range data {
		if b != '\n' {
			continue
		}
		if i > start {
			lines = append(lines, data[start:i])
		}
		start = i + 1
	}
	if start < len(data) {
		lines = append(lines, data[start:])
	}
	return lines
}
