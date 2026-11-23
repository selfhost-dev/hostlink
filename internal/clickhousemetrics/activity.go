package clickhousemetrics

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"hostlink/domain/metrics"
)

// Customer activity (selfhost#6038), read for a "stop when idle" policy. The
// agent collects as the platform user, and the platform's own work runs as
// that user too, so every read leaves that user out.

// idleWindow bounds every idle time: the first read covers the last day of
// query_log, and an instance idle for longer reads as a day.
const idleWindow = 24 * time.Hour

// queryLogLag re-reads the last minute on each incremental read: query_log
// flushes every 7.5 s, so a row can land after a read that ended "now".
const queryLogLag = 60 * time.Second

// newestCustomerQueryQuery is the newest query another user started since from.
// The first collection reads the last day; later ones only what is new, so a
// busy server's query_log is not scanned every 20 s.
func newestCustomerQueryQuery(platformUser string, from time.Time) string {
	return fmt.Sprintf("SELECT toUnixTimestamp(maxOrNull(event_time)) AS at FROM system.query_log "+
		"WHERE event_date >= toDate(fromUnixTimestamp(%d)) AND event_time > fromUnixTimestamp(%d) "+
		"AND type = 'QueryStart' AND user != %s FORMAT JSONEachRow", from.Unix(), from.Unix(), sqlString(platformUser))
}

// The newest active part of a table outside system: a write no query started
// (a Kafka-engine table's consumer, S3Queue, a refreshable view) still counts.
const newestCustomerPartQuery = "SELECT toUnixTimestamp(maxOrNull(modification_time)) AS at, uptime() AS uptime " +
	"FROM system.parts WHERE active AND database NOT IN ('system', 'INFORMATION_SCHEMA', 'information_schema') FORMAT JSONEachRow"

// Merges and mutations running on tables outside system: system's own *_log
// tables merge every few seconds and are not customer work.
const customerMergesQuery = "SELECT count() AS value FROM system.merges WHERE database != 'system' FORMAT JSONEachRow"

func customerRunningQuery(platformUser string) string {
	return fmt.Sprintf("SELECT count() AS value FROM system.processes WHERE user != %s FORMAT JSONEachRow", sqlString(platformUser))
}

// collectCustomerActivity fills the customer activity keys. A failed read omits
// its key: an unread idle time must never read as idle.
func (c *collector) collectCustomerActivity(baseURL, user, password string, m *metrics.ClickHouseDatabaseMetrics, now time.Time) {
	if idle, ok := c.customerIdleSeconds(baseURL, user, password, now); ok {
		m.CustomerIdleSeconds = &idle
	}
	if rows, err := c.query(baseURL, user, password, customerRunningQuery(user)); err == nil && len(rows) > 0 {
		m.CustomerRunningQueries = intPtr(rows[0]["value"])
	}
	if rows, err := c.query(baseURL, user, password, customerMergesQuery); err == nil && len(rows) > 0 {
		m.CustomerMerges = intPtr(rows[0]["value"])
	}
}

// customerIdleSeconds is the time since the newest customer query or write,
// capped at the server's uptime and at idleWindow.
func (c *collector) customerIdleSeconds(baseURL, user, password string, now time.Time) (int, bool) {
	if !c.readNewestCustomerQuery(baseURL, user, password, now) {
		return 0, false
	}
	rows, err := c.query(baseURL, user, password, newestCustomerPartQuery)
	if err != nil || len(rows) == 0 {
		return 0, false
	}

	idle := idleWindow
	if up := time.Duration(toInt64(rows[0]["uptime"])) * time.Second; up < idle {
		idle = up
	}
	for _, at := range []time.Time{c.customerQueryAt, unixOrZero(rows[0]["at"])} {
		if !at.IsZero() && now.Sub(at) < idle {
			idle = max(now.Sub(at), 0)
		}
	}
	return int(idle.Seconds()), true
}

// readNewestCustomerQuery advances customerQueryAt with the query_log rows since
// the previous read. A failed read keeps the window's start, so the next one
// covers it. query_log is created on its first flush: no table, no query logged.
func (c *collector) readNewestCustomerQuery(baseURL, user, password string, now time.Time) bool {
	from := c.queryLogFrom
	if from.IsZero() {
		from = now.Add(-idleWindow)
	}
	rows, err := c.query(baseURL, user, password, newestCustomerQueryQuery(user, from))
	var qErr *queryError
	switch {
	case err == nil:
		if len(rows) > 0 {
			if at := unixOrZero(rows[0]["at"]); at.After(c.customerQueryAt) {
				c.customerQueryAt = at
			}
		}
	case errors.As(err, &qErr) && qErr.Code == codeUnknownTable:
	default:
		return false
	}
	c.queryLogFrom = now.Add(-queryLogLag)
	return true
}

// unixOrZero reads a toUnixTimestamp column; null (nothing found) is zero.
func unixOrZero(v any) time.Time {
	if v == nil {
		return time.Time{}
	}
	if secs := toInt64(v); secs > 0 {
		return time.Unix(secs, 0)
	}
	return time.Time{}
}

// sqlString quotes s as a ClickHouse string literal.
func sqlString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
