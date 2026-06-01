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
	"hostlink/internal/containermetrics"
	"hostlink/internal/crypto"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/mongodbmetrics"
	"hostlink/internal/mysqlmetrics"
	"hostlink/internal/networkmetrics"
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
	apiserver          apiserver.MetricsOperations
	agentstate         agentstate.Operations
	metricscollector   pgmetrics.Collector
	syscollector       sysmetrics.Collector
	netcollector       networkmetrics.Collector
	storagecollector   storagemetrics.Collector
	pgbouncercollector pgbouncermetrics.Collector
	mysqlcollector     mysqlmetrics.Collector
	mongodbcollector   mongodbmetrics.Collector
	rediscollector     redismetrics.Collector
	containercollector containermetrics.Collector
	traefikcollector   traefikmetrics.Collector
	dockerDiscoverer   dockerdiscovery.Discoverer
	crypto             crypto.Service
	privateKeyPath     string
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
		apiserver:          svr,
		agentstate:         agentstate,
		metricscollector:   pgmetrics.New(),
		syscollector:       sysmetrics.New(),
		netcollector:       networkmetrics.New(),
		storagecollector:   storagemetrics.New(),
		pgbouncercollector: pgbouncermetrics.New(),
		mysqlcollector:     mysqlmetrics.New(),
		mongodbcollector:   mongodbmetrics.New(),
		rediscollector:     redismetrics.New(),
		containercollector: containermetrics.New(),
		traefikcollector:   traefikmetrics.New(),
		dockerDiscoverer:   dockerdiscovery.New(),
		crypto:             crypto.NewService(),
		privateKeyPath:     appconf.AgentPrivateKeyPath(),
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
	containercollector containermetrics.Collector,
	traefikcollector traefikmetrics.Collector,
	dockerDiscoverer dockerdiscovery.Discoverer,
	crypto crypto.Service,
	privateKeyPath string,
) *metricspusher {
	return &metricspusher{
		apiserver:          apiserver,
		agentstate:         agentstate,
		metricscollector:   pgcollector,
		syscollector:       syscollector,
		netcollector:       netcollector,
		storagecollector:   storagecollector,
		pgbouncercollector: pgbouncermetrics.New(),
		mysqlcollector:     mysqlcollector,
		mongodbcollector:   mongodbcollector,
		rediscollector:     rediscollector,
		containercollector: containercollector,
		traefikcollector:   traefikcollector,
		dockerDiscoverer:   dockerDiscoverer,
		crypto:             crypto,
		privateKeyPath:     privateKeyPath,
	}
}

func (mp *metricspusher) GetCreds() ([]credential.Credential, error) {
	agentID := mp.agentstate.GetAgentID()
	if agentID == "" {
		return nil, fmt.Errorf("agent not registered: missing agent ID")
	}

	creds, err := mp.apiserver.GetMetricsCreds(context.Background(), agentID)
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
			return nil, fmt.Errorf("failed to decrypt password for credential %d: %w", i, err)
		}
		creds[i].Password = &decPasswd
	}

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
	for _, d := range dockerDBs {
		switch d.Type {
		case dockerdiscovery.DatabaseTypePostgreSQL:
			mp.collectDockerPGMetrics(ctx, d, &metricSets)
		case dockerdiscovery.DatabaseTypeMySQL:
			mp.collectDockerMySQLMetrics(ctx, d, &metricSets)
		case dockerdiscovery.DatabaseTypeMongoDB:
			mp.collectDockerMongoDBMetrics(ctx, d, &metricSets)
		}
	}

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

	dbMetrics, err := mp.metricscollector.Collect(dockerCred)
	if err != nil {
		log.Warnf("docker PG metrics collection failed for %s: %v", d.ContainerName, err)
		dbMetrics = domainmetrics.PostgreSQLDatabaseMetrics{Up: false}
	} else {
		dbMetrics.Up = true
	}
	*metricSets = append(*metricSets, domainmetrics.MetricSet{
		Type:    domainmetrics.MetricTypePostgreSQLDatabase,
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

func (mp *metricspusher) collectDockerMySQLMetrics(ctx context.Context, d dockerdiscovery.DiscoveredDatabase, metricSets *[]domainmetrics.MetricSet) {
	dockerCred := credential.Credential{
		Host:     d.Host,
		Port:     int(d.Port),
		Username: d.Username,
		Database: d.Database,
		Dialect:  "mysql",
	}
	if d.Password != "" {
		dockerCred.Password = &d.Password
	}

	m, err := mp.mysqlcollector.Collect(dockerCred)
	if err != nil {
		log.Warnf("docker MySQL metrics collection failed for %s: %v", d.ContainerName, err)
		m = domainmetrics.MySQLDatabaseMetrics{Up: false}
	} else {
		m.Up = true
	}
	*metricSets = append(*metricSets, domainmetrics.MetricSet{
		Type:    domainmetrics.MetricTypeMySQLDatabase,
		Attributes: map[string]any{
			"container_id":   d.ContainerID[:12],
			"container_name": d.ContainerName,
			"database_name":  d.Database,
			"port":           d.Port,
			"source":         "docker",
		},
		Metrics: m,
	})
}

func (mp *metricspusher) collectDockerMongoDBMetrics(ctx context.Context, d dockerdiscovery.DiscoveredDatabase, metricSets *[]domainmetrics.MetricSet) {
	dockerCred := credential.Credential{
		Host:     d.Host,
		Port:     int(d.Port),
		Username: d.Username,
		Database: d.Database,
		Dialect:  "mongodb",
	}
	if d.Password != "" {
		dockerCred.Password = &d.Password
	}

	m, err := mp.mongodbcollector.Collect(dockerCred)
	if err != nil {
		log.Warnf("docker MongoDB metrics collection failed for %s: %v", d.ContainerName, err)
		m = domainmetrics.MongoDBMetrics{Up: false}
	} else {
		m.Up = true
	}
	*metricSets = append(*metricSets, domainmetrics.MetricSet{
		Type:    domainmetrics.MetricTypeMongoDBDatabase,
		Attributes: map[string]any{
			"container_id":   d.ContainerID[:12],
			"container_name": d.ContainerName,
			"database_name":  d.Database,
			"port":           d.Port,
			"source":         "docker",
		},
		Metrics: m,
	})
}

