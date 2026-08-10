// Package clickhousemetrics collects metrics from a ClickHouse instance via its HTTP interface.
package clickhousemetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
)

type Collector interface {
	Collect(credential.Credential) (metrics.ClickHouseDatabaseMetrics, error)
}

type collector struct {
	client         *http.Client
	lastQueries    *int64
	lastSelects    *int64
	lastInserts    *int64
	lastFailed     *int64
	lastInsertRows *int64
	lastSelectRows *int64
	lastTime       time.Time
}

func New() Collector {
	return &collector{client: &http.Client{Timeout: 10 * time.Second}}
}

func (c *collector) Collect(cred credential.Credential) (metrics.ClickHouseDatabaseMetrics, error) {
	password := ""
	if cred.Password != nil {
		password = *cred.Password
	}

	// ClickHouse default: TCP=9000, HTTP=8123 (diff=877). Derive HTTP port from
	// the TCP port stored in the credential. Fall back to 8123 for invalid values.
	httpPort := cred.Port - 877
	if httpPort <= 0 {
		httpPort = 8123
	}

	baseURL := fmt.Sprintf("http://%s:%d/", cred.Host, httpPort)

	if _, err := c.query(baseURL, cred.Username, password, "SELECT 1"); err != nil {
		return metrics.ClickHouseDatabaseMetrics{}, fmt.Errorf("ping: %w", err)
	}

	var m metrics.ClickHouseDatabaseMetrics

	// Point-in-time metrics from system.metrics
	sysRows, err := c.query(baseURL, cred.Username, password,
		"SELECT metric, value FROM system.metrics WHERE metric IN ('TCPConnection','HTTPConnection','MemoryTracking','BackgroundMergesAndMutationsPoolTask','BackgroundPoolTask') FORMAT JSONEachRow")
	if err != nil {
		return m, fmt.Errorf("system.metrics: %w", err)
	}
	for _, row := range sysRows {
		metricName, _ := row["metric"].(string)
		val := toInt64(row["value"])
		switch metricName {
		case "TCPConnection", "HTTPConnection":
			m.ConnectionsTotal += int(val)
		case "MemoryTracking":
			m.MemoryUsage = val
		case "BackgroundMergesAndMutationsPoolTask", "BackgroundPoolTask":
			m.BackgroundMergesCount += int(val)
		}
	}
	m.ConnectionsCount = m.ConnectionsTotal

	// Cumulative event counters used for delta rate calculation
	eventRows, err := c.query(baseURL, cred.Username, password,
		"SELECT event, value FROM system.events WHERE event IN ('Query','SelectQuery','InsertQuery','FailedQuery','InsertedRows','SelectedRows','MarkCacheHits','MarkCacheMisses') FORMAT JSONEachRow")
	if err != nil {
		return m, fmt.Errorf("system.events: %w", err)
	}
	events := make(map[string]int64, len(eventRows))
	for _, row := range eventRows {
		if k, ok := row["event"].(string); ok {
			events[k] = toInt64(row["value"])
		}
	}

	m.QueryCount = events["Query"]

	// Active MergeTree parts — a proxy for table fragmentation health
	partsRows, err := c.query(baseURL, cred.Username, password,
		"SELECT count() AS value FROM system.parts WHERE active = 1 FORMAT JSONEachRow")
	if err == nil && len(partsRows) > 0 {
		m.PartsActive = int(toInt64(partsRows[0]["value"]))
	}

	// Total disk space used by parts
	diskRows, err := c.query(baseURL, cred.Username, password,
		"SELECT sum(bytes_on_disk) AS value FROM system.parts FORMAT JSONEachRow")
	if err == nil && len(diskRows) > 0 {
		m.DiskUsedBytes = toInt64(diskRows[0]["value"])
	}

	// Detached/broken parts count
	detachedRows, err := c.query(baseURL, cred.Username, password,
		"SELECT count() AS value FROM system.detached_parts FORMAT JSONEachRow")
	if err == nil && len(detachedRows) > 0 {
		m.BrokenPartsCount = int(toInt64(detachedRows[0]["value"]))
	}

	// Replication delay from system.replicas (0 if standalone or no delay)
	replicaRows, err := c.query(baseURL, cred.Username, password,
		"SELECT max(absolute_delay) AS value FROM system.replicas FORMAT JSONEachRow")
	if err == nil && len(replicaRows) > 0 {
		delay := int(toInt64(replicaRows[0]["value"]))
		m.ReplicationDelay = delay
		m.ReplicationLagSeconds = delay
	}

	// Active replica count: distinct replica hosts participating in
	// replication. Omitted for standalone nodes (system.replicas is empty).
	replicaCountRows, err := c.query(baseURL, cred.Username, password,
		"SELECT uniqExact(hostname) AS value FROM system.replicas WHERE is_readonly = 0 FORMAT JSONEachRow")
	if err == nil && len(replicaCountRows) > 0 {
		count := int(toInt64(replicaCountRows[0]["value"]))
		m.ActiveReplicaCount = &count
	}

	// Mark cache: ratio of hits to total lookups.
	// Zero lookups (idle or insert-only node — inserts and system.* queries never
	// read MergeTree marks) means nothing was missed: report 100, mirroring the
	// PostgreSQL collector (blks_hit+blks_read = 0 → 100.0). Reporting 0 made
	// every quiet ClickHouse node look critical on a below-90% alert band while
	// an equally idle Postgres showed perfect.
	hits := events["MarkCacheHits"]
	misses := events["MarkCacheMisses"]
	if total := hits + misses; total > 0 {
		m.MarkCacheHitRatio = float64(hits) / float64(total) * 100
	} else {
		m.MarkCacheHitRatio = 100
	}

	// Delta-based rate metrics. First call stores the baseline and returns zero
	// for all rates (matching MySQL/PostgreSQL collector behaviour).
	now := time.Now()
	curQueries := events["Query"]
	curSelects := events["SelectQuery"]
	curInserts := events["InsertQuery"]
	curFailed := events["FailedQuery"]
	curInsertRows := events["InsertedRows"]
	curSelectRows := events["SelectedRows"]

	if c.lastQueries != nil {
		elapsed := now.Sub(c.lastTime).Seconds()
		if elapsed > 0 {
			m.QueriesPerSecond = float64(curQueries-*c.lastQueries) / elapsed
			m.SelectQueriesPerSecond = float64(curSelects-*c.lastSelects) / elapsed
			m.InsertQueriesPerSecond = float64(curInserts-*c.lastInserts) / elapsed
			m.FailedQueriesPerSecond = float64(curFailed-*c.lastFailed) / elapsed
			m.InsertedRowsPerSecond = float64(curInsertRows-*c.lastInsertRows) / elapsed
			if c.lastSelectRows != nil {
				m.SelectedRowsPerSecond = float64(curSelectRows-*c.lastSelectRows) / elapsed
			}
		}
	}

	c.lastQueries = &curQueries
	c.lastSelects = &curSelects
	c.lastInserts = &curInserts
	c.lastFailed = &curFailed
	c.lastInsertRows = &curInsertRows
	c.lastSelectRows = &curSelectRows
	c.lastTime = now

	return m, nil
}

// query sends a POST request to the ClickHouse HTTP interface and returns the
// parsed JSONEachRow response as a slice of maps (one map per result row).
func (c *collector) query(baseURL, user, password, q string) ([]map[string]any, error) {
	params := url.Values{}
	params.Set("user", user)
	params.Set("password", password)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		baseURL+"?"+params.Encode(),
		strings.NewReader(q))
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("clickhouse http %d: %s", resp.StatusCode, body)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rows []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(body)), "\n") {
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			return nil, fmt.Errorf("parse row: %w", err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func toInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case int64:
		return x
	case int:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}
