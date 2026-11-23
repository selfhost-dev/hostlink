package clickhousemetrics

import (
	"errors"
	"fmt"
	"strings"

	"hostlink/domain/metrics"
)

// Customer activity (selfhost#6038), read for a "stop when idle" policy. The
// agent collects as the platform user, and the platform's own work runs as
// that user too, so every read leaves that user out.

// idleWindowSeconds bounds the query_log read to its last day: an idle time
// longer than that reads as a day (and the server's uptime caps it anyway).
const idleWindowSeconds = 86400

func customerIdleQuery(platformUser string) string {
	return fmt.Sprintf("SELECT least(ifNull(dateDiff('second', maxOrNull(event_time), now()), %d), uptime()) AS idle "+
		"FROM system.query_log WHERE event_date >= yesterday() AND event_time > now() - INTERVAL 1 DAY "+
		"AND type = 'QueryStart' AND user != %s FORMAT JSONEachRow", idleWindowSeconds, sqlString(platformUser))
}

func customerRunningQuery(platformUser string) string {
	return fmt.Sprintf("SELECT count() AS value FROM system.processes WHERE user != %s FORMAT JSONEachRow", sqlString(platformUser))
}

// query_log is created on its first flush: until then nothing was logged, so
// the server has been idle since it started.
const uptimeQuery = "SELECT uptime() AS value FORMAT JSONEachRow"

// collectCustomerActivity fills the idle time and the running customer queries.
// A failed read omits its key: an unread idle time must never read as idle.
func (c *collector) collectCustomerActivity(baseURL, user, password string, m *metrics.ClickHouseDatabaseMetrics) {
	rows, err := c.query(baseURL, user, password, customerIdleQuery(user))
	var qErr *queryError
	switch {
	case err == nil && len(rows) > 0:
		m.CustomerQueryIdleSeconds = intPtr(rows[0]["idle"])
	case errors.As(err, &qErr) && qErr.Code == codeUnknownTable:
		if up, upErr := c.query(baseURL, user, password, uptimeQuery); upErr == nil && len(up) > 0 {
			m.CustomerQueryIdleSeconds = intPtr(up[0]["value"])
		}
	}

	if rows, err := c.query(baseURL, user, password, customerRunningQuery(user)); err == nil && len(rows) > 0 {
		m.CustomerRunningQueries = intPtr(rows[0]["value"])
	}
}

// sqlString quotes s as a ClickHouse string literal.
func sqlString(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}
