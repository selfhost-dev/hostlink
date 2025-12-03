package metrics

import (
	"context"
	"fmt"
	"os"
	"time"

	"hostlink/app/services/agentstate"
	"hostlink/config/appconf"
	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/apiserver"
	"hostlink/internal/cmdexec"
	"hostlink/internal/crypto"
	"hostlink/internal/pgmetrics"
	"hostlink/internal/sysmetrics"
)

type AuthGetter interface {
	GetCreds() ([]credential.Credential, error)
}

type Pusher interface {
	Push(credential.Credential) error
}

type metricspusher struct {
	apiserver        apiserver.MetricsOperations
	agentstate       agentstate.Operations
	metricscollector pgmetrics.Collector
	cmdExecutor      sysmetrics.CommandExecutor
	crypto           crypto.Service
	privateKeyPath   string
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
		apiserver:        svr,
		agentstate:       agentstate,
		metricscollector: pgmetrics.New(),
		cmdExecutor:      cmdexec.New(),
		crypto:           crypto.NewService(),
		privateKeyPath:   appconf.AgentPrivateKeyPath(),
	}, nil
}

func New() (*metricspusher, error) {
	return NewWithConf()
}

// NewWithDependencies allows full dependency injection for testing
func NewWithDependencies(
	apiserver apiserver.MetricsOperations,
	agentstate agentstate.Operations,
	collector pgmetrics.Collector,
	cmdExecutor sysmetrics.CommandExecutor,
	crypto crypto.Service,
	privateKeyPath string,
) *metricspusher {
	return &metricspusher{
		apiserver:        apiserver,
		agentstate:       agentstate,
		metricscollector: collector,
		cmdExecutor:      cmdExecutor,
		crypto:           crypto,
		privateKeyPath:   privateKeyPath,
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
	var collectionErrors []error

	sysCollector := sysmetrics.New(mp.cmdExecutor, sysmetrics.Config{
		DiskPath: cred.DataDirectory,
	})
	sysMetrics, err := sysCollector.Collect(ctx)
	if err != nil {
		collectionErrors = append(collectionErrors, fmt.Errorf("system metrics: %w", err))
	} else {
		metricSets = append(metricSets, domainmetrics.MetricSet{
			Type:    domainmetrics.MetricTypeSystem,
			Metrics: sysMetrics,
		})
	}

	dbMetrics, err := mp.metricscollector.Collect(cred)
	if err != nil {
		collectionErrors = append(collectionErrors, fmt.Errorf("database metrics: %w", err))
	} else {
		metricSets = append(metricSets, domainmetrics.MetricSet{
			Type:    domainmetrics.MetricTypePostgreSQLDatabase,
			Metrics: dbMetrics,
		})
	}

	if len(metricSets) == 0 {
		return fmt.Errorf("all metrics collection failed: %v", collectionErrors)
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
