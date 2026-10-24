package metrics

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/labstack/gommon/log"

	"hostlink/app/services/agentstate"
	"hostlink/config/appconf"
	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/apiserver"
	"hostlink/internal/clickhousemetrics"
	"hostlink/internal/containermetrics"
	"hostlink/internal/crypto"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/kafkametrics"
	"hostlink/internal/mongodbmetrics"
	"hostlink/internal/mysqlmetrics"
	"hostlink/internal/networkmetrics"
	"hostlink/internal/opensearchmetrics"
	"hostlink/internal/pgbouncermetrics"
	"hostlink/internal/pgmetrics"
	"hostlink/internal/redismetrics"
	"hostlink/internal/storagemetrics"
	"hostlink/internal/sysmetrics"
	"hostlink/internal/traefikmetrics"
)

type AuthGetter interface {
	GetCreds() ([]credential.Credential, error)
}

type Pusher interface {
	Push(credential.Credential) error
}

type metricspusher struct {
	apiserver           apiserver.MetricsOperations
	agentstate          agentstate.Operations
	metricscollector    pgmetrics.Collector
	syscollector        sysmetrics.Collector
	netcollector        networkmetrics.Collector
	storagecollector    storagemetrics.Collector
	pgbouncercollector  pgbouncermetrics.Collector
	mysqlcollector      mysqlmetrics.Collector
	mongodbcollector    mongodbmetrics.Collector
	rediscollector      redismetrics.Collector
	clickhousecollector clickhousemetrics.Collector
	opensearchcollector opensearchmetrics.Collector
	kafkacollector      kafkametrics.Collector
	containercollector  containermetrics.Collector
	traefikcollector    traefikmetrics.Collector
	dockerDiscoverer    dockerdiscovery.Discoverer
	crypto              crypto.Service
	privateKeyPath      string
	// credFetchTimeout bounds GetCreds; zero reads appconf.APICredentialsTimeout.
	credFetchTimeout time.Duration

	// One collector per discovered PostgreSQL container (a Coolify database),
	// keyed by container id: each owns that database's rate baseline.
	pgContainers   map[string]*pgContainerState
	newPGContainer func(containerID string) pgContainerCollector
	// The platform's credentials for Coolify database containers, by
	// container name, refreshed with every credential fetch.
	containerCreds map[string]credential.Credential
	roleEnsurer    roleEnsurer

	// MySQL / MongoDB / Redis containers (engine_container.go).
	engineContainers   map[string]*engineContainerState
	newEngineCollector func(containerID string, t dockerdiscovery.DatabaseType) engineCollectFunc
	engineEnsurer      func(ctx context.Context, t dockerdiscovery.DatabaseType, admin credential.Credential, user, password string) error
}

func NewWithConf() (*metricspusher, error) {
	agentstate := agentstate.New(appconf.AgentStatePath())
	if err := agentstate.Load(); err != nil {
		return nil, err
	}
	svr, err := apiserver.NewDefaultClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create request signer: %w", err)
	}

	return &metricspusher{
		apiserver:           svr,
		agentstate:          agentstate,
		metricscollector:    pgmetrics.New(),
		syscollector:        sysmetrics.New(),
		netcollector:        networkmetrics.New(),
		storagecollector:    storagemetrics.New(),
		pgbouncercollector:  pgbouncermetrics.New(),
		mysqlcollector:      mysqlmetrics.New(),
		mongodbcollector:    mongodbmetrics.New(),
		rediscollector:      redismetrics.New(),
		clickhousecollector: clickhousemetrics.New(),
		opensearchcollector: opensearchmetrics.New(),
		kafkacollector:      kafkametrics.New(),
		containercollector:  containermetrics.New(),
		traefikcollector:    traefikmetrics.New(),
		dockerDiscoverer:    dockerdiscovery.New(),
		crypto:              crypto.NewService(),
		privateKeyPath:      appconf.AgentPrivateKeyPath(),
	}, nil
}

func New() (*metricspusher, error) {
	return NewWithConf()
}

// NewWithDependencies allows full dependency injection for testing.
func NewWithDependencies(
	apiserver apiserver.MetricsOperations,
	agentstate agentstate.Operations,
	pgcollector pgmetrics.Collector,
	syscollector sysmetrics.Collector,
	netcollector networkmetrics.Collector,
	storagecollector storagemetrics.Collector,
	pgbouncercollector pgbouncermetrics.Collector,
	mysqlcollector mysqlmetrics.Collector,
	mongodbcollector mongodbmetrics.Collector,
	rediscollector redismetrics.Collector,
	clickhousecollector clickhousemetrics.Collector,
	opensearchcollector opensearchmetrics.Collector,
	kafkacollector kafkametrics.Collector,
	containercollector containermetrics.Collector,
	traefikcollector traefikmetrics.Collector,
	dockerDiscoverer dockerdiscovery.Discoverer,
	crypto crypto.Service,
	privateKeyPath string,
) *metricspusher {
	return &metricspusher{
		apiserver:           apiserver,
		agentstate:          agentstate,
		metricscollector:    pgcollector,
		syscollector:        syscollector,
		netcollector:        netcollector,
		storagecollector:    storagecollector,
		pgbouncercollector:  pgbouncercollector,
		mysqlcollector:      mysqlcollector,
		mongodbcollector:    mongodbcollector,
		rediscollector:      rediscollector,
		clickhousecollector: clickhousecollector,
		opensearchcollector: opensearchcollector,
		kafkacollector:      kafkacollector,
		containercollector:  containercollector,
		traefikcollector:    traefikcollector,
		dockerDiscoverer:    dockerDiscoverer,
		crypto:              crypto,
		privateKeyPath:      privateKeyPath,
	}
}

func (mp *metricspusher) GetCreds() ([]credential.Credential, error) {
	agentID := mp.agentstate.GetAgentID()
	if agentID == "" {
		return nil, fmt.Errorf("agent not registered: missing agent ID")
	}

	timeout := mp.credFetchTimeout
	if timeout <= 0 {
		timeout = appconf.APICredentialsTimeout()
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	creds, err := mp.apiserver.GetMetricsCreds(ctx, agentID)
	if err != nil {
		return nil, err
	}

	if len(creds) == 0 {
		return creds, nil
	}

	privateKey, err := mp.crypto.LoadPrivateKey(mp.privateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load private key: %w", err)
	}

	for i := range creds {
		encPasswd := creds[i].PasswdEnc
		if encPasswd == "" {
			continue
		}

		decPasswd, err := mp.crypto.DecryptWithPrivateKey(encPasswd, privateKey)
		if err != nil {
			// One Coolify database's credential must not stop every metric:
			// that container keeps its other logins, or reports
			// missing_credentials (see collectDockerPGMetrics).
			if creds[i].IsCoolify() {
				log.Warnj(log.JSON{"event": "credential_undecryptable", "credential": creds[i].ID, "error": err.Error()})
				continue
			}
			return nil, fmt.Errorf("failed to decrypt password for credential %d: %w", i, err)
		}
		creds[i].Password = &decPasswd
	}
	mp.decryptMonitorPasswords(creds, privateKey)
	mp.decryptAdminPasswords(creds, privateKey)
	mp.setContainerCreds(creds)

	return creds, nil
}

func (mp *metricspusher) Push(cred credential.Credential) error {
	agentID := mp.agentstate.GetAgentID()
	if agentID == "" {
		return fmt.Errorf("agent not registered: missing agent ID")
	}

	ctx := context.Background()
	var metricSets []domainmetrics.MetricSet

	// ── Infrastructure metrics (always collected) ────────────────────────────

	sysMetrics, err := mp.syscollector.Collect(ctx)
	if err != nil {
		log.Warnf("system metrics collection failed: %v", err)
	} else {
		metricSets = append(metricSets, domainmetrics.MetricSet{
			Type:    domainmetrics.MetricTypeSystem,
			Metrics: sysMetrics,
		})
	}

	netMetrics, err := mp.netcollector.Collect(ctx)
	if err != nil {
		log.Warnf("network metrics collection failed: %v", err)
	} else {
		metricSets = append(metricSets, domainmetrics.MetricSet{
			Type:    domainmetrics.MetricTypeNetwork,
			Metrics: netMetrics,
		})
	}

	// ── Database metrics (dispatched by credential dialect) ──────────────────

	switch cred.Dialect {
	case "mysql", "mariadb":
		if cred.Host != "" || cred.Port != 0 {
			m, err := mp.mysqlcollector.Collect(cred)
			if err != nil {
				log.Warnf("mysql metrics collection failed: %v", err)
				m = domainmetrics.MySQLDatabaseMetrics{Up: false}
			} else {
				m.Up = true
			}
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:    domainmetrics.MetricTypeMySQLDatabase,
				Metrics: m,
			})
		}

	case "mongodb":
		if cred.Host != "" || cred.Port != 0 {
			m, err := mp.mongodbcollector.Collect(cred)
			if err != nil {
				log.Warnf("mongodb metrics collection failed: %v", err)
				m = domainmetrics.MongoDBMetrics{Up: false}
			} else {
				m.Up = true
			}
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:    domainmetrics.MetricTypeMongoDBDatabase,
				Metrics: m,
			})
		}

	case "redis":
		if cred.Host != "" || cred.Port != 0 {
			m, err := mp.rediscollector.Collect(cred)
			if err != nil {
				log.Warnf("redis metrics collection failed: %v", err)
				m = domainmetrics.RedisMetrics{Up: false}
			} else {
				m.Up = true
			}
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:    domainmetrics.MetricTypeRedis,
				Metrics: m,
			})
		}

	case "clickhouse":
		if cred.Host != "" || cred.Port != 0 {
			m, err := mp.clickhousecollector.Collect(cred)
			if err != nil {
				log.Warnf("clickhouse metrics collection failed: %v", err)
				// Topology is not known for a node the agent could not query.
				m = domainmetrics.ClickHouseDatabaseMetrics{
					Up:                false,
					Mode:              domainmetrics.ClickHouseModeUnknown,
					KeeperStatus:      domainmetrics.ClickHouseKeeperUnknown,
					ReplicationStatus: domainmetrics.ClickHouseReplicationUnknown,
				}
			} else {
				m.Up = true
			}
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:    domainmetrics.MetricTypeClickHouseDatabase,
				Metrics: m,
			})
		}

	case "opensearch":
		if cred.Host != "" || cred.Port != 0 {
			m, err := mp.opensearchcollector.Collect(cred)
			if err != nil {
				log.Warnf("opensearch metrics collection failed: %v", err)
				m = domainmetrics.OpenSearchDatabaseMetrics{Up: false, ClusterStatus: 2}
			} else {
				m.Up = true
			}
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:    domainmetrics.MetricTypeOpenSearchDatabase,
				Metrics: m,
			})
		}

	case "kafka":
		// AutoMQ exposes Prometheus on the box; the collector scrapes localhost:9090
		// and reads consumer-group lag over the localhost INTERNAL listener via the
		// admin API (cred is unused). Collect never errors — a down broker returns
		// Up=false; an unreadable lag omits the consumer-group keys (selfhost#2854).
		if cred.Host != "" || cred.Port != 0 {
			m, _ := mp.kafkacollector.Collect(cred)
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:    domainmetrics.MetricTypeKafkaDatabase,
				Metrics: m,
			})
		}

	default: // "postgresql", "supabase", or empty — existing behaviour
		hasPrimaryCred := cred.Host != "" || cred.Port != 0
		if hasPrimaryCred {
			dbMetrics, err := mp.metricscollector.Collect(cred)
			if err != nil {
				log.Warnf("database metrics collection failed: %v", err)
				dbMetrics = domainmetrics.PostgreSQLDatabaseMetrics{Up: false}
			} else {
				dbMetrics.Up = true
			}
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:    domainmetrics.MetricTypePostgreSQLDatabase,
				Metrics: dbMetrics,
			})

			// PgBouncer is co-located with PostgreSQL — try-connect, silently skip if absent
			pgbouncerMetrics, err := mp.pgbouncercollector.Collect(cred)
			if err != nil {
				pgbouncerMetrics = domainmetrics.PgBouncerMetrics{Up: false}
			} else {
				pgbouncerMetrics.Up = true
			}
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:    domainmetrics.MetricTypePgBouncer,
				Metrics: pgbouncerMetrics,
			})
		}
	}

	// ── Docker-discovered database containers ────────────────────────────────

	dockerDBs, err := mp.dockerDiscoverer.DiscoverDatabases(ctx)
	if err != nil {
		log.Warnf("docker database discovery failed: %v", err)
	}
	seenPG := map[string]bool{}
	seenEngine := map[string]bool{}
	for _, d := range dockerDBs {
		switch d.Type {
		case dockerdiscovery.DatabaseTypePostgreSQL:
			seenPG[d.ContainerID] = true
			mp.collectDockerPGMetrics(ctx, d, &metricSets)
		case dockerdiscovery.DatabaseTypeMySQL, dockerdiscovery.DatabaseTypeMongoDB, dockerdiscovery.DatabaseTypeRedis:
			seenEngine[d.ContainerID] = true
			mp.collectDockerEngine(ctx, d, &metricSets)
		}
	}
	mp.prunePGContainers(seenPG)
	mp.pruneEngineContainers(seenEngine)

	// ── Storage metrics ──────────────────────────────────────────────────────

	storageMetrics, err := mp.storagecollector.Collect(ctx)
	if err != nil {
		log.Warnf("storage metrics collection failed: %v", err)
	} else {
		for _, sm := range storageMetrics {
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type: domainmetrics.MetricTypeStorage,
				Attributes: map[string]any{
					"mount_point":     sm.Attributes.MountPoint,
					"device":          sm.Attributes.Device,
					"filesystem_type": sm.Attributes.FilesystemType,
					"is_read_only":    sm.Attributes.IsReadOnly,
				},
				Metrics: sm.Metrics,
			})
		}
	}

	// ── Container metrics (Coolify apps) ─────────────────────────────────────

	containerSets, err := mp.containercollector.Collect(ctx)
	if err != nil {
		log.Warnf("container metrics collection failed: %v", err)
	} else {
		for _, cm := range containerSets {
			attrs := map[string]any{
				"container_id":   cm.Attributes.ContainerID,
				"container_name": cm.Attributes.ContainerName,
				"image":          cm.Attributes.Image,
			}
			// Omit empty Coolify metadata
			if cm.Attributes.CoolifyAppID != "" {
				attrs["coolify_app_id"] = cm.Attributes.CoolifyAppID
			}
			if cm.Attributes.CoolifyProjectID != "" {
				attrs["coolify_project_id"] = cm.Attributes.CoolifyProjectID
			}
			if cm.Attributes.CoolifyEnvironmentID != "" {
				attrs["coolify_environment_id"] = cm.Attributes.CoolifyEnvironmentID
			}
			if cm.Attributes.CoolifyType != "" {
				attrs["coolify_type"] = cm.Attributes.CoolifyType
			}
			if cm.Attributes.CoolifyName != "" {
				attrs["coolify_name"] = cm.Attributes.CoolifyName
			}
			if cm.Attributes.CoolifyServiceID != "" {
				attrs["coolify_service_id"] = cm.Attributes.CoolifyServiceID
			}

			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:       domainmetrics.MetricTypeContainer,
				Attributes: attrs,
				Metrics:    cm.Metrics,
			})
		}
	}

	// ── Traefik metrics (HTTP requests / response time / error rate per entrypoint) ──

	traefikSets, err := mp.traefikcollector.Collect(ctx)
	if err != nil {
		log.Warnf("traefik metrics collection failed: %v", err)
	} else {
		for _, ts := range traefikSets {
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type: domainmetrics.MetricTypeTraefikService,
				Attributes: map[string]any{
					"entrypoint_name": ts.Attributes.EntrypointName,
				},
				Metrics: ts.Metrics,
			})
		}
	}

	// ── Traefik router metrics (per-app, excludes catchall noise) ────────────────

	routerSets, err := mp.traefikcollector.CollectRouters(ctx)
	if err != nil {
		log.Warnf("traefik router metrics collection failed: %v", err)
	} else {
		for _, rs := range routerSets {
			attrs := map[string]any{
				"router_name":     rs.Attributes.RouterName,
				"entrypoint_name": rs.Attributes.EntrypointName,
			}
			if rs.Attributes.Service != "" {
				attrs["service"] = rs.Attributes.Service
			}
			metricSets = append(metricSets, domainmetrics.MetricSet{
				Type:       domainmetrics.MetricTypeTraefikRouter,
				Attributes: attrs,
				Metrics:    rs.Metrics,
			})
		}
	}

	hostname, _ := os.Hostname()

	payload := domainmetrics.MetricPayload{
		Version:     "1.0",
		TimestampMs: time.Now().UnixMilli(),
		Resource: domainmetrics.Resource{
			AgentID:  agentID,
			HostName: hostname,
		},
		MetricSets: metricSets,
	}

	return mp.apiserver.PushMetrics(ctx, payload)
}
