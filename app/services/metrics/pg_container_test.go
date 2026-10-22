package metrics

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/pgmetrics"
	"hostlink/internal/storagemetrics"
)

// ── Coolify (Docker-discovered) PostgreSQL ───────────────────────────────────

type fakePGContainer struct {
	calls  int
	cred   credential.Credential
	result domainmetrics.PostgreSQLContainerMetrics
	fail   *pgmetrics.Failure
}

func (f *fakePGContainer) Collect(cred credential.Credential) (domainmetrics.PostgreSQLContainerMetrics, *pgmetrics.Failure) {
	f.calls++
	f.cred = cred
	return f.result, f.fail
}

// A Coolify host: no primary database credential, only discovered containers.
func setupCoolifyHost(t *testing.T, dbs []dockerdiscovery.DiscoveredDatabase) (*metricspusher, *testMocks, map[string]*fakePGContainer, *domainmetrics.MetricPayload) {
	t.Helper()
	mp, mocks := setupTestMetricsPusher()
	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	mocks.storagecollector.On("Collect", mock.Anything).Return([]storagemetrics.StorageMetricSet(nil), nil)
	mocks.dockerDiscoverer.ExpectedCalls = nil
	mocks.dockerDiscoverer.On("DiscoverDatabases", mock.Anything).Return(dbs, nil)

	fakes := map[string]*fakePGContainer{}
	mp.newPGContainer = func(containerID string) pgContainerCollector {
		f := &fakePGContainer{}
		fakes[containerID] = f
		return f
	}

	var pushed domainmetrics.MetricPayload
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		pushed = args.Get(1).(domainmetrics.MetricPayload)
	}).Return(nil)
	return mp, mocks, fakes, &pushed
}

func coolifyPG(id string) dockerdiscovery.DiscoveredDatabase {
	return dockerdiscovery.DiscoveredDatabase{
		Type: dockerdiscovery.DatabaseTypePostgreSQL, ContainerID: id + "000000000000", ContainerName: id,
		Host: "10.0.1.7", Port: 5432, Database: "appdb", Username: "app", Password: "pw",
	}
}

func pgSet(t *testing.T, p *domainmetrics.MetricPayload, containerName string) domainmetrics.MetricSet {
	t.Helper()
	for _, ms := range p.MetricSets {
		if ms.Type == domainmetrics.MetricTypePostgreSQLDatabase && ms.Attributes["container_name"] == containerName {
			return ms
		}
	}
	t.Fatalf("no postgresql.database set for %s", containerName)
	return domainmetrics.MetricSet{}
}

// The container is reached at the address discovery resolved (its Docker
// network IP), and its health payload is pushed as the postgresql.database
// set with the same attributes the platform attributes it by.
func TestPush_CoolifyPostgres_PushesHealthPayloadAtTheContainerAddress(t *testing.T) {
	mp, _, fakes, pushed := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")})
	up := true
	conns := 4
	mp.newPGContainer = func(containerID string) pgContainerCollector {
		f := &fakePGContainer{result: domainmetrics.PostgreSQLContainerMetrics{
			Up: &up, Health: domainmetrics.HealthHealthy, CollectionStatus: domainmetrics.CollectionComplete, ConnectionsTotal: &conns,
		}}
		fakes[containerID] = f
		return f
	}

	assert.NoError(t, mp.Push(credential.Credential{}))

	f := fakes["dbuuid1000000000000"]
	assert.Equal(t, "10.0.1.7", f.cred.Host)
	assert.Equal(t, 5432, f.cred.Port)
	assert.Equal(t, "pw", *f.cred.Password)

	set := pgSet(t, pushed, "dbuuid1")
	assert.Equal(t, "docker", set.Attributes["source"])
	assert.Equal(t, "appdb", set.Attributes["database_name"])
	body, _ := json.Marshal(set.Metrics)
	assert.Contains(t, string(body), `"up":true`)
	assert.Contains(t, string(body), `"health":"healthy"`)
	assert.Contains(t, string(body), `"connections_total":4`)
}

// An unknown state is pushed as such (up null), not flattened to up:false
// with zeros.
func TestPush_CoolifyPostgres_UnknownIsNotFlattenedToDown(t *testing.T) {
	mp, _, fakes, pushed := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")})
	mp.newPGContainer = func(containerID string) pgContainerCollector {
		f := &fakePGContainer{
			result: domainmetrics.PostgreSQLContainerMetrics{Health: domainmetrics.HealthUnknown, CollectionStatus: domainmetrics.CollectionFailed, CollectionError: "auth_failed"},
			fail:   &pgmetrics.Failure{Stage: "connect", Kind: "auth_failed", Err: errors.New("pq: password authentication failed")},
		}
		fakes[containerID] = f
		return f
	}

	assert.NoError(t, mp.Push(credential.Credential{}))

	body, _ := json.Marshal(pgSet(t, pushed, "dbuuid1").Metrics)
	assert.Contains(t, string(body), `"up":null`)
	assert.Contains(t, string(body), `"health":"unknown"`)
	assert.Contains(t, string(body), `"collection_error":"auth_failed"`)
	assert.Contains(t, string(body), `"connections_total":null`)
	assert.NotContains(t, string(body), "password authentication", "the raw error stays in the agent log")
}

// One collector per container, kept across pushes (it owns the rate
// baseline). A container missing from fewer than pgPruneAfter discoveries (a
// Docker API hiccup) keeps its collector; one gone longer gets a fresh one if
// it ever comes back.
func TestPush_CoolifyPostgres_OneCollectorPerContainerAcrossPushes(t *testing.T) {
	dbs := []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1"), coolifyPG("dbuuid2")}
	mp, mocks, _, _ := setupCoolifyHost(t, dbs)
	created := map[string]int{}
	mp.newPGContainer = func(containerID string) pgContainerCollector {
		created[containerID]++
		return &fakePGContainer{}
	}
	discover := func(found []dockerdiscovery.DiscoveredDatabase) {
		mocks.dockerDiscoverer.ExpectedCalls = nil
		mocks.dockerDiscoverer.On("DiscoverDatabases", mock.Anything).Return(found, nil)
		assert.NoError(t, mp.Push(credential.Credential{}))
	}

	discover(dbs)
	discover(dbs)
	assert.Equal(t, map[string]int{"dbuuid1000000000000": 1, "dbuuid2000000000000": 1}, created)

	for i := 0; i < pgPruneAfter-1; i++ {
		discover(dbs[:1]) // a transient gap
	}
	discover(dbs)
	assert.Equal(t, 1, created["dbuuid2000000000000"], "a short gap keeps the baseline")

	for i := 0; i < pgPruneAfter; i++ {
		discover(dbs[:1]) // gone
	}
	discover(dbs) // and back
	assert.Equal(t, 2, created["dbuuid2000000000000"], "a container gone long enough starts fresh")
}
