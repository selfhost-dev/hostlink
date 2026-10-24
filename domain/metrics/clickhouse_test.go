package metrics

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// legacyClickHouseKeys is every key a clickhouse.database payload carried
// before the topology fields (#283), with the JSON kind the console and the
// control plane parse it as. None may be renamed, retyped or dropped: the
// console reads the numeric ones as `num?`, and customer alert rules name them.
var legacyClickHouseKeys = map[string]string{
	"up":                        "bool",
	"connections_total":         "number",
	"connections_count":         "number",
	"query_count":               "number",
	"queries_per_second":        "number",
	"select_queries_per_second": "number",
	"selected_rows_per_second":  "number",
	"insert_queries_per_second": "number",
	"failed_queries_per_second": "number",
	"inserted_rows_per_second":  "number",
	"memory_usage":              "number",
	"background_merges_count":   "number",
	"replication_delay":         "number",
	"replication_lag_seconds":   "number",
	"disk_used_bytes":           "number",
	"broken_parts_count":        "number",
	"mark_cache_hit_ratio":      "number",
	"parts_active":              "number",
	"keeper_connected":          "bool",
}

func jsonKind(v any) string {
	switch v.(type) {
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case nil:
		return "null"
	}
	return "other"
}

func marshalToMap(t *testing.T, m ClickHouseDatabaseMetrics) map[string]any {
	t.Helper()
	raw, err := json.Marshal(m)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestClickHouseDatabaseMetrics_JSON_KeepsEveryLegacyKeyAndType(t *testing.T) {
	for _, tc := range []struct {
		name string
		m    ClickHouseDatabaseMetrics
	}{
		{"zero value (database down)", ClickHouseDatabaseMetrics{}},
		{"standalone", ClickHouseDatabaseMetrics{
			Up: true, Mode: ClickHouseModeStandalone,
			KeeperStatus:      ClickHouseKeeperNotApplicable,
			ReplicationStatus: ClickHouseReplicationNotApplicable,
		}},
		{"replicated", ClickHouseDatabaseMetrics{
			Up: true, KeeperConnected: true, ReplicationDelay: 5, ReplicationLagSeconds: 5,
			Mode:              ClickHouseModeReplicated,
			KeeperStatus:      ClickHouseKeeperConnected,
			ReplicationStatus: ClickHouseReplicationOK,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := marshalToMap(t, tc.m)
			for key, kind := range legacyClickHouseKeys {
				v, ok := got[key]
				if assert.Truef(t, ok, "legacy key %q missing", key) {
					assert.Equalf(t, kind, jsonKind(v), "legacy key %q changed JSON type", key)
				}
			}
			// Absent today (the collector never set it); must stay absent.
			assert.NotContains(t, got, "active_replica_count")
		})
	}
}

func TestClickHouseDatabaseMetrics_JSON_TopologyFields(t *testing.T) {
	tables, broken := 3, 1
	got := marshalToMap(t, ClickHouseDatabaseMetrics{
		Mode:                     ClickHouseModeReplicated,
		KeeperStatus:             ClickHouseKeeperDisconnected,
		ReplicationStatus:        ClickHouseReplicationLagging,
		ReplicatedTablesCount:    &tables,
		BrokenDetachedPartsCount: &broken,
	})
	assert.Equal(t, "replicated", got["mode"])
	assert.Equal(t, "disconnected", got["keeper_status"])
	assert.Equal(t, "lagging", got["replication_status"])
	assert.Equal(t, float64(3), got["replicated_tables_count"])
	assert.Equal(t, float64(1), got["broken_detached_parts_count"])

	// Counts that could not be read are omitted, never a made-up 0.
	empty := marshalToMap(t, ClickHouseDatabaseMetrics{})
	assert.NotContains(t, empty, "replicated_tables_count")
	assert.NotContains(t, empty, "broken_detached_parts_count")
}

func TestClickHouseStatusVocabulary(t *testing.T) {
	assert.Equal(t, "standalone", ClickHouseModeStandalone)
	assert.Equal(t, "replicated", ClickHouseModeReplicated)
	assert.Equal(t, "unknown", ClickHouseModeUnknown)
	assert.Equal(t, "connected", ClickHouseKeeperConnected)
	assert.Equal(t, "disconnected", ClickHouseKeeperDisconnected)
	assert.Equal(t, "not_applicable", ClickHouseKeeperNotApplicable)
	assert.Equal(t, "unknown", ClickHouseKeeperUnknown)
	assert.Equal(t, "ok", ClickHouseReplicationOK)
	assert.Equal(t, "lagging", ClickHouseReplicationLagging)
	assert.Equal(t, "not_applicable", ClickHouseReplicationNotApplicable)
	assert.Equal(t, "unknown", ClickHouseReplicationUnknown)
}
