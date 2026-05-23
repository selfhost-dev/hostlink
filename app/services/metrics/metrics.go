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
	"hostlink/internal/crypto"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/networkmetrics"
	"hostlink/internal/pgbouncermetrics"
	"hostlink/internal/pgmetrics"
	"hostlink/internal/storagemetrics"
	"hostlink/internal/sysmetrics"
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
		dockerDiscoverer:   dockerdiscovery.New(),
		crypto:             crypto.NewService(),
		privateKeyPath:     appconf.AgentPrivateKeyPath(),
	}, nil
}

func New() (*metricspusher, error) {
	return NewWithConf()
}

// NewWithDependencies allows full dependency injection for testing
func NewWithDependencies(
	apiserver apiserver.MetricsOperations,
	agentstate agentstate.Operations,
	pgcollector pgmetrics.Collector,
	syscollector sysmetrics.Collector,
	netcollector networkmetrics.Collector,
	storagecollector storagemetrics.Collector,
	pgbouncercollector pgbouncermetrics.Collector,
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
		pgbouncercollector: pgbouncercollector,
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

	// Collect from control-plane credential (primary PG)
	hasPrimaryCred := cred.Host != "" || cred.Port != 0
	if hasPrimaryCred {
		dbMetrics, err := mp.metricscollector.Collect(cred)
		if err != nil {
			log.Warnf("primary database metrics collection failed: %v", err)
			dbMetrics = domainmetrics.PostgreSQLDatabaseMetrics{Up: false}
		} else {
			dbMetrics.Up = true
		}
		metricSets = append(metricSets, domainmetrics.MetricSet{
			Type:    domainmetrics.MetricTypePostgreSQLDatabase,
			Metrics: dbMetrics,
		})

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

	// Discover Docker containers and collect PG metrics from each
	dockerDBs, err := mp.dockerDiscoverer.DiscoverDatabases(ctx)
	if err != nil {
		log.Warnf("docker database discovery failed: %v", err)
	}
	for _, d := range dockerDBs {
		switch d.Type {
		case dockerdiscovery.DatabaseTypePostgreSQL:
			mp.collectDockerPGMetrics(ctx, d, &metricSets)
		default:
			// MySQL and MongoDB collectors not yet implemented
		}
	}

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
