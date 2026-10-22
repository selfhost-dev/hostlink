package metrics

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/labstack/gommon/log"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/pgmetrics"
	"hostlink/internal/pgmonitorrole"
)

type pgContainerCollector interface {
	Collect(credential.Credential) (domainmetrics.PostgreSQLContainerMetrics, *pgmetrics.Failure)
}

type pgContainerState struct {
	collector    pgContainerCollector
	lastKind     string
	lastLog      time.Time
	missed       int // consecutive pushes the container was not discovered in
	lastRepair   time.Time
	repairedWith string // the monitoring password the last repair set
}

// repairDue: at most every pgRepairEvery, unless the platform's monitoring
// password changed since the last repair (another writer replaced it), which
// is repaired at once instead of leaving monitoring blind for the window.
func (s *pgContainerState) repairDue(password string) bool {
	if password != s.repairedWith || time.Since(s.lastRepair) >= pgRepairEvery {
		s.lastRepair, s.repairedWith = time.Now(), password
		return true
	}
	return false
}

// needsRepair: the role's login was refused, for its password (auth_failed)
// or for a missing CONNECT privilege (permission_denied at connect, e.g. after
// REVOKE CONNECT ... FROM PUBLIC).
func needsRepair(f *pgmetrics.Failure) bool {
	return f != nil && f.Stage == "connect" && (f.Kind == "auth_failed" || f.Kind == "permission_denied")
}

// pgRepairTimeout bounds a repair: a role locked by a customer's open
// transaction must never stall the metrics loop.
const pgRepairTimeout = 30 * time.Second

// roleEnsurer creates or repairs the monitoring role inside a container
// (pgmonitorrole.Ensurer).
type roleEnsurer interface {
	Ensure(ctx context.Context, containerID, superuser, database, role, password string) error
}

// pgRepairEvery: a failed monitoring login triggers a role repair at most this
// often per container, so a repair that cannot succeed is not run every push.
const pgRepairEvery = 10 * time.Minute

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

	// A database the platform names (matched by its uuid, wherever it sits in
	// the credential list) is logged in to with what the platform supplies,
	// never the container's env, which goes stale once the password changes:
	// the agent's own role when it has that login, else the platform's record
	// of the customer's password. The container's own address and port are
	// used either way; the credential's localhost/public port are not.
	platform, named := mp.containerCred(d.ContainerName)
	monitored := named && platform.MonitorUsername != "" && platform.MonitorPassword != nil
	switch {
	case monitored:
		dockerCred.Username = platform.MonitorUsername
		dockerCred.Password = platform.MonitorPassword
	case named && platform.Password != nil:
		dockerCred.Username = platform.Username
		dockerCred.Password = platform.Password
	}
	if named && platform.Database != "" {
		dockerCred.Database = platform.Database
	}

	state := mp.pgContainer(d.ContainerID)
	var dbMetrics domainmetrics.PostgreSQLContainerMetrics
	var failure *pgmetrics.Failure
	if named && dockerCred.Password == nil {
		// A platform database with no usable password anywhere (undecryptable,
		// and none in the env): say so, never a login that reads as zeros.
		dbMetrics, failure = pgmetrics.Unavailable("missing_credentials", errors.New("no usable credential for the container"))
	} else {
		dbMetrics, failure = state.collector.Collect(dockerCred)
		if monitored && needsRepair(failure) && state.repairDue(*platform.MonitorPassword) {
			if mp.repairMonitorRole(ctx, d, platform) {
				dbMetrics, failure = state.collector.Collect(dockerCred)
			}
		}
	}
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

// repairMonitorRole creates the monitoring role, or restores it (a database
// provisioned before it existed, or a customer who changed it), through the
// container's local socket as the container's bootstrap superuser.
func (mp *metricspusher) repairMonitorRole(ctx context.Context, d dockerdiscovery.DiscoveredDatabase, platform credential.Credential) bool {
	if mp.roleEnsurer == nil {
		ensurer, err := pgmonitorrole.NewDocker()
		if err != nil {
			log.Warnj(log.JSON{"event": "pg_monitor_role_failed", "container": d.ContainerName, "error": err.Error()})
			return false
		}
		mp.roleEnsurer = ensurer
	}
	superuser := d.Username
	if superuser == "" {
		superuser = "postgres"
	}
	database := platform.Database
	if database == "" {
		database = d.Database
	}

	ctx, cancel := context.WithTimeout(ctx, pgRepairTimeout)
	defer cancel()
	if err := mp.roleEnsurer.Ensure(ctx, d.ContainerID, superuser, database, platform.MonitorUsername, *platform.MonitorPassword); err != nil {
		log.Warnj(log.JSON{
			"event": "pg_monitor_role_failed", "container": d.ContainerName, "database": database,
			"role": platform.MonitorUsername, "error": pgmetrics.SanitizeError(err),
		})
		return false
	}
	log.Infoj(log.JSON{"event": "pg_monitor_role_ensured", "container": d.ContainerName, "database": database, "role": platform.MonitorUsername})
	return true
}

// setContainerCreds keeps the platform's credentials for Coolify PostgreSQL
// containers, by container name (the database uuid).
func (mp *metricspusher) setContainerCreds(creds []credential.Credential) {
	byName := map[string]credential.Credential{}
	for _, c := range creds {
		if c.Dialect == "postgresql" && c.ContainerName != "" {
			byName[c.ContainerName] = c
		}
	}
	mp.containerCreds = byName
}

// containerCred: Coolify names a standalone database's container after its
// uuid (a suffixed name, uuid-<n>, is the same database).
func (mp *metricspusher) containerCred(containerName string) (credential.Credential, bool) {
	if c, ok := mp.containerCreds[containerName]; ok {
		return c, true
	}
	for name, c := range mp.containerCreds {
		if strings.HasPrefix(containerName, name+"-") {
			return c, true
		}
	}
	return credential.Credential{}, false
}

// decryptMonitorPasswords: a monitoring password that cannot be decrypted
// leaves that container on the platform's customer login; it never fails the
// fetch.
func (mp *metricspusher) decryptMonitorPasswords(creds []credential.Credential, privateKey *rsa.PrivateKey) {
	for i := range creds {
		if creds[i].MonitorPasswdEnc == "" {
			continue
		}
		password, err := mp.crypto.DecryptWithPrivateKey(creds[i].MonitorPasswdEnc, privateKey)
		if err != nil {
			log.Warnj(log.JSON{"event": "pg_monitor_password_undecryptable", "credential": creds[i].ID, "error": err.Error()})
			continue
		}
		creds[i].MonitorPassword = &password
	}
}
