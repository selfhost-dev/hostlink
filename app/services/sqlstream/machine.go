package sqlstream

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The checks the job path's script makes before it runs (selfhost
// script/clickhouse/clickhouse_sql_run.sh): the account as its last sync left it, its runs
// logged, its limits sized for this machine. Each refusal is the same code the script gives, and
// the backend mends each with a sync on the job path.

const (
	accountFile = "z_selfhost_sql_editor.xml"
	limitsFile  = "z_selfhost_sql_editor_limits.xml"
)

// machine reads the account's files and this machine's size.
type machine struct {
	usersDir string

	mu       sync.Mutex
	sizedAt  time.Time
	ramMB    string
	cores    string
	sizeTTL  time.Duration
	sizeFunc func() (ramMB, cores string)
}

func newMachine(usersDir string) *machine {
	return &machine{usersDir: usersDir, sizeTTL: 5 * time.Minute, sizeFunc: machineSize}
}

// check returns "" when the account may run, or the refusal's code.
func (m *machine) check(user, stamp string) string {
	data, err := os.ReadFile(filepath.Join(m.usersDir, accountFile))
	if err != nil {
		return "role_out_of_date"
	}
	first, _, _ := bytes.Cut(data, []byte("\n"))
	want := fmt.Sprintf("<!-- %s sync %s -->", user, stamp)
	if string(bytes.TrimRight(first, "\r")) != want {
		return "role_out_of_date"
	}
	if !bytes.Contains(data, []byte("<log_queries>1</log_queries>")) {
		return "role_logging_changed"
	}
	if !m.limitsCurrent() {
		return "limits_out_of_date"
	}
	return ""
}

// limitsCurrent is clickhouse_sql_editor_limits_current: no limits file, or a machine whose size
// cannot be read, is not stale; a file sized for another machine is.
func (m *machine) limitsCurrent() bool {
	f, err := os.Open(filepath.Join(m.usersDir, limitsFile))
	if err != nil {
		return true
	}
	defer f.Close()
	first, err := bufio.NewReader(f).ReadString('\n')
	if err != nil && first == "" {
		return false
	}
	ramMB, cores := m.size()
	if !allDigits(ramMB) || !allDigits(cores) {
		return true
	}
	return strings.TrimRight(first, "\r\n") == fmt.Sprintf("<!-- selfhost_sql_editor limits for %s MB and %s vCPUs -->", ramMB, cores)
}

func (m *machine) size() (string, string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ramMB == "" || time.Since(m.sizedAt) > m.sizeTTL {
		m.ramMB, m.cores = m.sizeFunc()
		m.sizedAt = time.Now()
	}
	return m.ramMB, m.cores
}

// machineSize reads the RAM and the vCPUs exactly as the scripts do (free -m's total, getconf
// _NPROCESSORS_ONLN), with their overrides, so the two never disagree about a machine.
func machineSize() (string, string) {
	ramMB := os.Getenv("SELFHOST_TOTAL_RAM_MB")
	if ramMB == "" {
		if out, err := exec.Command("free", "-m").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				f := strings.Fields(line)
				if len(f) >= 2 && f[0] == "Mem:" {
					ramMB = f[1]
				}
			}
		}
	}
	cores := os.Getenv("SELFHOST_CPU_CORES")
	if cores == "" {
		if out, err := exec.Command("getconf", "_NPROCESSORS_ONLN").Output(); err == nil {
			cores = strings.TrimSpace(string(out))
		}
	}
	return ramMB, cores
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}
