package sqlstream

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestMachineChecks(t *testing.T) {
	dir := t.TempDir()
	m := newMachine(dir)
	m.sizeFunc = func() (string, string) { return "3834", "2" }

	if got := m.check("selfhost_read", "42"); got != "role_out_of_date" {
		t.Fatalf("no account file: %q", got)
	}
	writeFile(t, dir, accountFile, "<!-- selfhost_read sync 41 -->\n<clickhouse><log_queries>1</log_queries></clickhouse>\n")
	if got := m.check("selfhost_read", "42"); got != "role_out_of_date" {
		t.Fatalf("another sync's file: %q", got)
	}
	writeFile(t, dir, accountFile, "<!-- selfhost_read sync 42 -->\n<clickhouse></clickhouse>\n")
	if got := m.check("selfhost_read", "42"); got != "role_logging_changed" {
		t.Fatalf("not logging: %q", got)
	}
	writeFile(t, dir, accountFile, "<!-- selfhost_read sync 42 -->\n<clickhouse><log_queries>1</log_queries></clickhouse>\n")
	if got := m.check("selfhost_read", "42"); got != "" {
		t.Fatalf("no limits file is fine: %q", got)
	}
	writeFile(t, dir, limitsFile, "<!-- selfhost_sql_editor limits for 3834 MB and 2 vCPUs -->\n<clickhouse/>\n")
	if got := m.check("selfhost_read", "42"); got != "" {
		t.Fatalf("limits for this machine: %q", got)
	}
	writeFile(t, dir, limitsFile, "<!-- selfhost_sql_editor limits for 7838 MB and 4 vCPUs -->\n<clickhouse/>\n")
	m.sizedAt = m.sizedAt.Add(-m.sizeTTL * 2)
	if got := m.check("selfhost_read", "42"); got != "limits_out_of_date" {
		t.Fatalf("limits for another machine: %q", got)
	}
	// A machine whose size cannot be read is not stale, as the script decides.
	m.sizeFunc = func() (string, string) { return "", "2" }
	m.sizedAt = m.sizedAt.Add(-m.sizeTTL * 2)
	if got := m.check("selfhost_read", "42"); got != "" {
		t.Fatalf("unreadable size: %q", got)
	}
}
