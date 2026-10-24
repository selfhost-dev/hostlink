package clickhousemetrics

import (
	"errors"
	"strings"

	"hostlink/domain/metrics"
)

// replicationLagThresholdSeconds splits replication_status "ok" from
// "lagging": max(absolute_delay) above it is lagging. 60s is the platform's
// existing in-sync threshold (selfhost CloudProvider::Aws::ReplicationStatusService
// REPLICATION_LAG_THRESHOLD_SECONDS, used by WaitReplicationSyncJob and by
// Clickhouse::TopologyHealth when it judges an older agent's lag). absolute_delay
// is coarse (seconds behind the oldest unprocessed queue entry), so a few
// seconds during a large fetch or merge is normal and must not read as lagging.
const replicationLagThresholdSeconds = 60

const (
	// keeperProbeQuery is unchanged from before #283: keeper_connected keeps
	// its exact meaning. Only a non-empty root reads as connected.
	keeperProbeQuery = "SELECT count() AS value FROM system.zookeeper WHERE path='/' FORMAT JSONEachRow"

	// replicasQuery reads the table count and the lag from one snapshot, so
	// replication_status always judges the lag it reports. max() over an
	// empty table is 0, which is what replication_delay has always carried.
	replicasQuery = "SELECT count() AS tables, max(absolute_delay) AS max_delay FROM system.replicas FORMAT JSONEachRow"

	// brokenDetachedPartsQuery counts detached parts ClickHouse set aside as
	// broken (reasons broken, broken-on-start, broken-from-backup), not parts
	// detached on purpose (DETACH PART, ignored, unexpected, noquorum...).
	// reason is Nullable(String); ifNull keeps the condition non-nullable.
	brokenDetachedPartsQuery = "SELECT countIf(ifNull(reason, '') LIKE 'broken%') AS value FROM system.detached_parts FORMAT JSONEachRow"
)

// keeperStatus classifies the result of keeperProbeQuery.
//
// Keeper is "not configured" only on a definite server signal:
//   - NO_ZOOKEEPER (225), or
//   - NO_ELEMENTS_IN_CONFIG (139) with the "no Zookeeper configuration"
//     message, which is what 26.8 actually raises for system.zookeeper on a
//     node without a Keeper section. 139 is a generic code, so the message is
//     required too; if a future release rewords it, the verdict degrades to
//     unknown, never to a false not_applicable.
//
// system.zookeeper_connection is not queried: on 26.8 it calls the same
// Context::getZooKeeper and fails the same way, so it adds no signal.
//
// KEEPER_EXCEPTION (999) comes only from the Keeper client, which exists only
// when Keeper is configured: disconnected. A readable but empty root is no
// quorum: disconnected. Anything else (permissions, HTTP timeout, a proxy
// error, no code) is unknown.
func keeperStatus(rows []map[string]any, err error) string {
	if err == nil {
		if len(rows) == 0 {
			return metrics.ClickHouseKeeperUnknown
		}
		if toInt64(rows[0]["value"]) > 0 {
			return metrics.ClickHouseKeeperConnected
		}
		return metrics.ClickHouseKeeperDisconnected
	}

	var qe *queryError
	if !errors.As(err, &qe) {
		return metrics.ClickHouseKeeperUnknown
	}
	switch {
	case keeperNotConfigured(qe):
		return metrics.ClickHouseKeeperNotApplicable
	case qe.Code == codeKeeperException:
		return metrics.ClickHouseKeeperDisconnected
	}
	return metrics.ClickHouseKeeperUnknown
}

func keeperNotConfigured(qe *queryError) bool {
	switch qe.Code {
	case codeNoZooKeeper:
		return true
	case codeNoElementsInConfig:
		return strings.Contains(strings.ToLower(qe.Message), "no zookeeper configuration")
	}
	return false
}

// applyReplication fills the legacy lag fields (same values as before #283)
// and the replication verdict from replicasQuery's result.
//
// When system.replicas cannot be read, mode is "unknown" too: guessing
// standalone would hide an HA node, guessing replicated would invent one.
func applyReplication(m *metrics.ClickHouseDatabaseMetrics, rows []map[string]any, err error) {
	if err != nil || len(rows) == 0 {
		m.Mode = metrics.ClickHouseModeUnknown
		m.ReplicationStatus = metrics.ClickHouseReplicationUnknown
		return
	}
	rawTables, ok := rows[0]["tables"]
	if !ok {
		m.Mode = metrics.ClickHouseModeUnknown
		m.ReplicationStatus = metrics.ClickHouseReplicationUnknown
		return
	}

	delay := int(toInt64(rows[0]["max_delay"]))
	m.ReplicationDelay = delay
	m.ReplicationLagSeconds = delay

	tables := int(toInt64(rawTables))
	m.ReplicatedTablesCount = &tables

	switch {
	case tables == 0:
		m.Mode = metrics.ClickHouseModeStandalone
		m.ReplicationStatus = metrics.ClickHouseReplicationNotApplicable
	case delay > replicationLagThresholdSeconds:
		m.Mode = metrics.ClickHouseModeReplicated
		m.ReplicationStatus = metrics.ClickHouseReplicationLagging
	default:
		m.Mode = metrics.ClickHouseModeReplicated
		m.ReplicationStatus = metrics.ClickHouseReplicationOK
	}
}

// reconcileKeeper: replicated tables need Keeper. A node that has them but no
// Keeper configuration has every one of them read-only, the same outcome as a
// lost Keeper, so it reads disconnected rather than not_applicable.
//
// And with Keeper disconnected, lag says nothing yet: absolute_delay only
// grows past the threshold a minute after the queue stops moving, so "ok"
// would sit beside read-only tables. Replication then reads unknown. An
// unknown Keeper (a permission error on the probe, say) leaves the lag
// verdict alone: that can persist, and the lag is still a real reading.
func reconcileKeeper(m *metrics.ClickHouseDatabaseMetrics) {
	if m.KeeperStatus == metrics.ClickHouseKeeperNotApplicable && m.Mode == metrics.ClickHouseModeReplicated {
		m.KeeperStatus = metrics.ClickHouseKeeperDisconnected
	}
	if m.KeeperStatus == metrics.ClickHouseKeeperDisconnected && m.Mode == metrics.ClickHouseModeReplicated {
		m.ReplicationStatus = metrics.ClickHouseReplicationUnknown
	}
}
