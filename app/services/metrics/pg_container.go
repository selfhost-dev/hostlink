package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/labstack/gommon/log"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/pgmetrics"
)

type pgContainerCollector interface {
	Collect(credential.Credential) (domainmetrics.PostgreSQLContainerMetrics, *pgmetrics.Failure)
}

type pgContainerState struct {
	collector pgContainerCollector
	lastKind  string
	lastLog   time.Time
	missed    int // consecutive pushes the container was not discovered in
}

// pgPruneAfter: a container missing from this many consecutive discoveries
// is gone. Discovery skips a container whose list or inspect call failed, so
// one Docker API hiccup must not drop its baseline.
const pgPruneAfter = 3

// pgFailureLogEvery: a failure that keeps happening is logged again at most
// this often (the push runs every 20 seconds); a change of failure, and the
// recovery, are logged at once.
const pgFailureLogEvery = 5 * time.Minute

// collectDockerPGMetrics collects a Docker-discovered PostgreSQL container
// (a Coolify database) with its own pgmetrics.ContainerCollector, which says
// healthy / unhealthy / unknown instead of flattening every failure to
// up:false with zeros. The managed-instance path above keeps
// PostgreSQLDatabaseMetrics: its up drives the platform's failover.
func (mp *metricspusher) collectDockerPGMetrics(ctx context.Context, d dockerdiscovery.DiscoveredDatabase, metricSets *[]domainmetrics.MetricSet) {
	dockerCred := credential.Credential{
		Host:     d.Host,
		Port:     int(d.Port),
		Username: d.Username,
		Database: d.Database,
		Dialect:  "postgresql",
	}
	if d.Password != "" {
		dockerCred.Password = &d.Password
	}

	state := mp.pgContainer(d.ContainerID)
	dbMetrics, failure := state.collector.Collect(dockerCred)
	mp.logPGContainer(state, d, dbMetrics, failure)

	*metricSets = append(*metricSets, domainmetrics.MetricSet{
		Type: domainmetrics.MetricTypePostgreSQLDatabase,
		Attributes: map[string]any{
			"container_id":   d.ContainerID[:12],
			"container_name": d.ContainerName,
			"database_name":  d.Database,
			"port":           d.Port,
			"source":         "docker",
		},
		Metrics: dbMetrics,
	})
}

func (mp *metricspusher) pgContainer(containerID string) *pgContainerState {
	if mp.pgContainers == nil {
		mp.pgContainers = map[string]*pgContainerState{}
	}
	if state, ok := mp.pgContainers[containerID]; ok {
		return state
	}
	newCollector := mp.newPGContainer
	if newCollector == nil {
		newCollector = func(string) pgContainerCollector { return pgmetrics.NewContainerCollector() }
	}
	state := &pgContainerState{collector: newCollector(containerID)}
	mp.pgContainers[containerID] = state
	return state
}

// prunePGContainers drops the collector of a container missing from
// pgPruneAfter consecutive discoveries, so a removed database's baseline and
// failure state do not linger, and a transient discovery gap keeps them.
func (mp *metricspusher) prunePGContainers(seen map[string]bool) {
	for id, state := range mp.pgContainers {
		if seen[id] {
			state.missed = 0
			continue
		}
		state.missed++
		if state.missed >= pgPruneAfter {
			delete(mp.pgContainers, id)
		}
	}
}

// logPGContainer: structured, sanitized, and never the password. Identifies
// the container and database, the stage and the category.
func (mp *metricspusher) logPGContainer(state *pgContainerState, d dockerdiscovery.DiscoveredDatabase,
	m domainmetrics.PostgreSQLContainerMetrics, failure *pgmetrics.Failure) {
	if failure == nil {
		if state.lastKind != "" {
			log.Infoj(log.JSON{
				"event": "pg_container_metrics_recovered", "container": d.ContainerName, "database": d.Database,
				"previous_error": state.lastKind, "duration_ms": m.CollectionDurationMs,
			})
			state.lastKind = ""
		}
		return
	}

	now := time.Now()
	if failure.Kind == state.lastKind && now.Sub(state.lastLog) < pgFailureLogEvery {
		return
	}
	state.lastKind, state.lastLog = failure.Kind, now
	entry := log.JSON{
		"event": "pg_container_metrics_failed", "container": d.ContainerName, "database": d.Database,
		"address": fmt.Sprintf("%s:%d", d.Host, d.Port), "stage": failure.Stage, "kind": failure.Kind,
		"health": m.Health, "collection_status": m.CollectionStatus, "duration_ms": m.CollectionDurationMs,
		"error": pgmetrics.SanitizeError(failure.Err),
	}
	if failure.Metric != "" {
		entry["metric"] = failure.Metric
	}
	if m.LastSuccessAt != nil {
		entry["last_success_at"] = *m.LastSuccessAt
	}
	log.Warnj(entry)
}

// hostOrLocalhost keeps the MySQL and MongoDB containers' behaviour from
// before discovery could say "no address": they still try localhost. Only
// the PostgreSQL path reports no_address.
func hostOrLocalhost(host string) string {
	if host == "" {
		return "localhost"
	}
	return host
}
