package metrics

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/labstack/gommon/log"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/dbhealth"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/enginemonitor"
	"hostlink/internal/mongodbmetrics"
	"hostlink/internal/mysqlmetrics"
	"hostlink/internal/pgmetrics"
	"hostlink/internal/redismetrics"
)

// The Docker path for Coolify MySQL, MongoDB and Redis containers, held to the
// same rules as PostgreSQL (pg_container.go):
//
//   - one collector per container (MongoDB's ops/sec baseline was shared);
//   - a container the platform names (container_name = the database uuid) is
//     logged in to with what the platform supplies: the agent's own
//     selfhost_monitor user (MySQL, MongoDB), else the platform's record of
//     the customer's login; never the container's env, which goes stale once
//     the password changes. An unnamed container (a template's) keeps its env;
//   - the payload is a health verdict (dbhealth): up true / false / null,
//     health, collection_status, collection_error, last_success_at. A failed
//     collection carries no numbers (absent, read as null), never zeros;
//   - the monitoring user is created or repaired as the platform's admin
//     (MySQL root, MongoDB root) when its login is refused, at most every
//     pgRepairEvery or at once on a new password, bounded by pgRepairTimeout;
//     when that fails, the platform's customer login is used instead.

type engineCollectFunc func(credential.Credential) (any, error)

type engineContainerState struct {
	collect         engineCollectFunc
	lastSuccess     time.Time
	lastKind        string
	lastLog         time.Time
	missed          int
	lastRepair      time.Time
	repairedWith    string
	userUnavailable bool
}

func (s *engineContainerState) repairPending(password string) bool {
	return password != s.repairedWith || time.Since(s.lastRepair) >= pgRepairEvery
}

func (s *engineContainerState) repairDue(password string) bool {
	if !s.repairPending(password) {
		return false
	}
	s.lastRepair, s.repairedWith = time.Now(), password
	return true
}

var engineMetricTypes = map[dockerdiscovery.DatabaseType]string{
	dockerdiscovery.DatabaseTypeMySQL:   domainmetrics.MetricTypeMySQLDatabase,
	dockerdiscovery.DatabaseTypeMongoDB: domainmetrics.MetricTypeMongoDBDatabase,
	dockerdiscovery.DatabaseTypeRedis:   domainmetrics.MetricTypeRedis,
}

// engineHasMonitorUser: Redis has no durable runtime user (ACL users vanish on
// restart), so it runs on the platform's password alone.
func engineHasMonitorUser(t dockerdiscovery.DatabaseType) bool {
	return t == dockerdiscovery.DatabaseTypeMySQL || t == dockerdiscovery.DatabaseTypeMongoDB
}

func newEngineCollect(_ string, t dockerdiscovery.DatabaseType) engineCollectFunc {
	switch t {
	case dockerdiscovery.DatabaseTypeMySQL:
		c := mysqlmetrics.New()
		return func(cred credential.Credential) (any, error) { return c.Collect(cred) }
	case dockerdiscovery.DatabaseTypeMongoDB:
		c := mongodbmetrics.New()
		return func(cred credential.Credential) (any, error) { return c.Collect(cred) }
	default:
		c := redismetrics.New()
		return func(cred credential.Credential) (any, error) { return c.Collect(cred) }
	}
}

func ensureEngineUser(ctx context.Context, t dockerdiscovery.DatabaseType, admin credential.Credential, user, password string) error {
	switch t {
	case dockerdiscovery.DatabaseTypeMySQL:
		return enginemonitor.EnsureMySQL(ctx, admin, user, password)
	case dockerdiscovery.DatabaseTypeMongoDB:
		return enginemonitor.EnsureMongo(ctx, admin, user, password)
	}
	return nil
}

func (mp *metricspusher) collectDockerEngine(ctx context.Context, d dockerdiscovery.DiscoveredDatabase, metricSets *[]domainmetrics.MetricSet) {
	env := credential.Credential{Host: d.Host, Port: int(d.Port), Username: d.Username, Database: d.Database, Dialect: string(d.Type)}
	if d.Password != "" {
		env.Password = &d.Password
	}

	platform, named := mp.containerCred(d.ContainerName)
	monitored := named && engineHasMonitorUser(d.Type) && platform.MonitorUsername != "" && platform.MonitorPassword != nil
	customer := env
	if named && platform.Password != nil {
		customer.Username, customer.Password = platform.Username, platform.Password
		if platform.Database != "" {
			customer.Database = platform.Database
		}
	}
	monitor := env
	if monitored {
		// Database-less: the monitoring user has no per-database grants.
		monitor.Username, monitor.Password, monitor.Database = platform.MonitorUsername, platform.MonitorPassword, ""
	}

	state := mp.engineContainer(d.ContainerID, d.Type)
	start := time.Now()
	var result any
	var err error
	switch {
	case d.Host == "":
		err = errNoAddress
	case !monitored && customer.Password == nil && named:
		err = errMissingCredentials
	case !monitored:
		result, err = state.collect(customer)
	default:
		result, err = mp.collectMonitored(ctx, state, d, monitor, customer, platform)
	}

	payload := engineVerdict(state, result, err, time.Since(start))
	mp.logEngineContainer(state, d, payload, err)
	*metricSets = append(*metricSets, domainmetrics.MetricSet{
		Type: engineMetricTypes[d.Type],
		Attributes: map[string]any{
			"container_id":   d.ContainerID[:12],
			"container_name": d.ContainerName,
			"database_name":  d.Database,
			"port":           d.Port,
			"source":         "docker",
		},
		Metrics: payload,
	})
}

var (
	errNoAddress          = errors.New("no address reaches the container")
	errMissingCredentials = errors.New("no usable credential for the container")
)

func (mp *metricspusher) collectMonitored(ctx context.Context, state *engineContainerState, d dockerdiscovery.DiscoveredDatabase,
	monitor, customer, platform credential.Credential) (any, error) {
	canFallBack := customer.Password != nil
	password := *platform.MonitorPassword
	if state.userUnavailable && canFallBack && !state.repairPending(password) {
		return state.collect(customer)
	}

	result, err := state.collect(monitor)
	if err == nil || !engineNeedsRepair(err) {
		if err == nil {
			state.userUnavailable = false
		}
		return result, err
	}

	if state.repairDue(password) {
		if mp.repairEngineUser(ctx, d, platform) {
			state.userUnavailable = false
			if result, err = state.collect(monitor); err == nil || !engineNeedsRepair(err) {
				return result, err
			}
		}
		state.userUnavailable = true
	}
	if canFallBack {
		return state.collect(customer)
	}
	return result, err
}

// engineNeedsRepair: the monitoring user's login was refused, for its
// password or a privilege.
func engineNeedsRepair(err error) bool {
	kind, _, _ := dbhealth.Classify(err)
	return kind == "auth_failed" || kind == "permission_denied"
}

func (mp *metricspusher) repairEngineUser(ctx context.Context, d dockerdiscovery.DiscoveredDatabase, platform credential.Credential) bool {
	if platform.AdminUsername == "" || platform.AdminPassword == nil {
		log.Warnj(log.JSON{"event": "engine_monitor_user_failed", "container": d.ContainerName, "engine": string(d.Type), "error": enginemonitor.ErrNoAdmin.Error()})
		return false
	}
	admin := credential.Credential{Host: d.Host, Port: int(d.Port), Username: platform.AdminUsername, Password: platform.AdminPassword}
	ensure := mp.engineEnsurer
	if ensure == nil {
		ensure = ensureEngineUser
	}
	ctx, cancel := context.WithTimeout(ctx, pgRepairTimeout)
	defer cancel()
	if err := ensure(ctx, d.Type, admin, platform.MonitorUsername, *platform.MonitorPassword); err != nil {
		log.Warnj(log.JSON{"event": "engine_monitor_user_failed", "container": d.ContainerName, "engine": string(d.Type),
			"user": platform.MonitorUsername, "error": pgmetrics.SanitizeError(err)})
		return false
	}
	log.Infoj(log.JSON{"event": "engine_monitor_user_ensured", "container": d.ContainerName, "engine": string(d.Type), "user": platform.MonitorUsername})
	return true
}

// engineVerdict builds the payload: the collector's numbers plus the verdict
// on success; the verdict alone (no numbers) on failure.
func engineVerdict(state *engineContainerState, result any, err error, took time.Duration) map[string]any {
	payload := map[string]any{}
	if err == nil {
		if body, mErr := json.Marshal(result); mErr == nil {
			_ = json.Unmarshal(body, &payload)
		}
		state.lastSuccess = time.Now()
		payload["up"] = true
		payload["health"] = dbhealth.Healthy
		payload["collection_status"] = domainmetrics.CollectionComplete
	} else {
		kind, down, connected := dbhealth.Classify(err)
		switch {
		case errors.Is(err, errNoAddress):
			kind, down, connected = "no_address", false, false
		case errors.Is(err, errMissingCredentials):
			kind, down, connected = "missing_credentials", false, false
		}
		up, health := dbhealth.Verdict(kind, down, connected)
		payload["up"] = up
		payload["health"] = health
		payload["collection_status"] = domainmetrics.CollectionFailed
		payload["collection_error"] = kind
	}
	payload["collection_duration_ms"] = took.Milliseconds()
	if state.lastSuccess.IsZero() {
		payload["last_success_at"] = nil
	} else {
		payload["last_success_at"] = state.lastSuccess.UTC().Format(time.RFC3339)
	}
	return payload
}

func (mp *metricspusher) engineContainer(containerID string, t dockerdiscovery.DatabaseType) *engineContainerState {
	if mp.engineContainers == nil {
		mp.engineContainers = map[string]*engineContainerState{}
	}
	if s, ok := mp.engineContainers[containerID]; ok {
		return s
	}
	factory := mp.newEngineCollector
	if factory == nil {
		factory = newEngineCollect
	}
	s := &engineContainerState{collect: factory(containerID, t)}
	mp.engineContainers[containerID] = s
	return s
}

// pruneEngineContainers: a container missing from pgPruneAfter consecutive
// discoveries is gone.
func (mp *metricspusher) pruneEngineContainers(seen map[string]bool) {
	for id, s := range mp.engineContainers {
		if seen[id] {
			s.missed = 0
			continue
		}
		if s.missed++; s.missed >= pgPruneAfter {
			delete(mp.engineContainers, id)
		}
	}
}

func (mp *metricspusher) logEngineContainer(state *engineContainerState, d dockerdiscovery.DiscoveredDatabase, payload map[string]any, err error) {
	if err == nil {
		if state.lastKind != "" {
			log.Infoj(log.JSON{"event": "engine_container_metrics_recovered", "engine": string(d.Type), "container": d.ContainerName, "previous_error": state.lastKind})
			state.lastKind = ""
		}
		return
	}
	kind, _ := payload["collection_error"].(string)
	now := time.Now()
	if kind == state.lastKind && now.Sub(state.lastLog) < pgFailureLogEvery {
		return
	}
	state.lastKind, state.lastLog = kind, now
	log.Warnj(log.JSON{
		"event": "engine_container_metrics_failed", "engine": string(d.Type), "container": d.ContainerName,
		"address": fmt.Sprintf("%s:%d", d.Host, d.Port), "kind": kind, "health": payload["health"],
		"error": pgmetrics.SanitizeError(err), "last_success_at": payload["last_success_at"],
	})
}

// decryptAdminPasswords: an admin password that cannot be decrypted only
// means the monitoring user cannot be created; it never fails the fetch.
func (mp *metricspusher) decryptAdminPasswords(creds []credential.Credential, privateKey *rsa.PrivateKey) {
	for i := range creds {
		if creds[i].AdminPasswdEnc == "" {
			continue
		}
		password, err := mp.crypto.DecryptWithPrivateKey(creds[i].AdminPasswdEnc, privateKey)
		if err != nil {
			log.Warnj(log.JSON{"event": "admin_password_undecryptable", "credential": creds[i].ID, "error": err.Error()})
			continue
		}
		creds[i].AdminPassword = &password
	}
}
