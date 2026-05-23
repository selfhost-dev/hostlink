package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var mu sync.Mutex

func Event(name string, fields map[string]any) {
	write(merge(fields, map[string]any{"event": name}))
}

func Metric(name string, value any, fields map[string]any) {
	write(merge(fields, map[string]any{"metric_name": name, "value": value}))
}

func write(entry map[string]any) {
	path := os.Getenv("HOSTLINK_WS_TELEMETRY_PATH")
	if path == "" {
		return
	}
	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	entry["recorded_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = file.Write(append(data, '\n'))
}

func merge(fields map[string]any, base map[string]any) map[string]any {
	merged := map[string]any{}
	for key, value := range fields {
		merged[key] = value
	}
	for key, value := range base {
		merged[key] = value
	}
	return merged
}
