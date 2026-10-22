package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

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

// ── the monitoring login (selfhost_monitor) ──────────────────────────────────

type fakeRoleEnsurer struct {
	calls                            int
	containerID, superuser, database string
	role, password                   string
	err                              error
}

func (f *fakeRoleEnsurer) Ensure(_ context.Context, containerID, superuser, database, role, password string) error {
	f.calls++
	f.containerID, f.superuser, f.database, f.role, f.password = containerID, superuser, database, role, password
	return f.err
}

// A collector whose results are scripted per call.
type scriptedPG struct {
	creds   []credential.Credential
	results []domainmetrics.PostgreSQLContainerMetrics
	fails   []*pgmetrics.Failure
}

func (s *scriptedPG) Collect(cred credential.Credential) (domainmetrics.PostgreSQLContainerMetrics, *pgmetrics.Failure) {
	i := len(s.creds)
	s.creds = append(s.creds, cred)
	if i >= len(s.results) {
		i = len(s.results) - 1
	}
	return s.results[i], s.fails[i]
}

func monitorCred(container string) credential.Credential {
	pw := "monitor-pw"
	return credential.Credential{Dialect: "postgresql", Host: "localhost", ContainerName: container,
		Username: "customer", MonitorUsername: "selfhost_monitor", MonitorPassword: &pw}
}

var (
	healthy    = domainmetrics.PostgreSQLContainerMetrics{Health: domainmetrics.HealthHealthy, CollectionStatus: domainmetrics.CollectionComplete}
	authFailed = domainmetrics.PostgreSQLContainerMetrics{Health: domainmetrics.HealthUnknown, CollectionStatus: domainmetrics.CollectionFailed, CollectionError: "auth_failed"}
	authErr    = &pgmetrics.Failure{Stage: "connect", Kind: "auth_failed", Err: errors.New("pq: password authentication failed")}
)

// The container selfhost names is collected as selfhost_monitor with the
// platform's password, at the container's own address: never as the customer,
// whose password may have changed.
func TestPush_CoolifyPostgres_CollectsAsTheMonitoringRole(t *testing.T) {
	mp, _, _, _ := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")})
	pg := &scriptedPG{results: []domainmetrics.PostgreSQLContainerMetrics{healthy}, fails: []*pgmetrics.Failure{nil}}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	mp.setContainerCreds([]credential.Credential{monitorCred("dbuuid1")})

	assert.NoError(t, mp.Push(credential.Credential{}))

	require.Len(t, pg.creds, 1)
	assert.Equal(t, "selfhost_monitor", pg.creds[0].Username)
	assert.Equal(t, "monitor-pw", *pg.creds[0].Password)
	assert.Equal(t, "10.0.1.7", pg.creds[0].Host)
	assert.Equal(t, "appdb", pg.creds[0].Database)
}

// The role is missing (an existing database) or was changed: the agent
// creates or repairs it through the container's local socket, as the
// container's superuser, and collects again.
func TestPush_CoolifyPostgres_RepairsTheRoleOnAFailedLoginAndRetries(t *testing.T) {
	mp, _, _, pushed := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")})
	pg := &scriptedPG{
		results: []domainmetrics.PostgreSQLContainerMetrics{authFailed, healthy},
		fails:   []*pgmetrics.Failure{authErr, nil},
	}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	ensurer := &fakeRoleEnsurer{}
	mp.roleEnsurer = ensurer
	mp.setContainerCreds([]credential.Credential{monitorCred("dbuuid1")})

	assert.NoError(t, mp.Push(credential.Credential{}))

	assert.Equal(t, 1, ensurer.calls)
	assert.Equal(t, "dbuuid1000000000000", ensurer.containerID)
	assert.Equal(t, "app", ensurer.superuser, "the container's POSTGRES_USER, over the trusted local socket")
	assert.Equal(t, "appdb", ensurer.database)
	assert.Equal(t, "selfhost_monitor", ensurer.role)
	assert.Equal(t, "monitor-pw", ensurer.password)
	assert.Len(t, pg.creds, 2, "collected again after the repair")
	body, _ := json.Marshal(pgSet(t, pushed, "dbuuid1").Metrics)
	assert.Contains(t, string(body), `"health":"healthy"`)
}

// A repair that keeps failing is not retried on every push.
func TestPush_CoolifyPostgres_RoleRepairIsRateLimited(t *testing.T) {
	mp, _, _, _ := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")})
	pg := &scriptedPG{results: []domainmetrics.PostgreSQLContainerMetrics{authFailed}, fails: []*pgmetrics.Failure{authErr}}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	ensurer := &fakeRoleEnsurer{err: errors.New("psql exited 2")}
	mp.roleEnsurer = ensurer
	mp.setContainerCreds([]credential.Credential{monitorCred("dbuuid1")})

	for i := 0; i < 3; i++ {
		assert.NoError(t, mp.Push(credential.Credential{}))
	}

	assert.Equal(t, 1, ensurer.calls)
}

// A container selfhost does not name (a Supabase or template Postgres) keeps
// the container's own env credentials, and no role is created in it.
func TestPush_UnnamedContainerKeepsItsEnvCredentialsAndGetsNoRole(t *testing.T) {
	mp, _, _, _ := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("supabase-db")})
	pg := &scriptedPG{results: []domainmetrics.PostgreSQLContainerMetrics{authFailed}, fails: []*pgmetrics.Failure{authErr}}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	ensurer := &fakeRoleEnsurer{}
	mp.roleEnsurer = ensurer
	mp.setContainerCreds([]credential.Credential{monitorCred("dbuuid1")})

	assert.NoError(t, mp.Push(credential.Credential{}))

	assert.Equal(t, "app", pg.creds[0].Username)
	assert.Equal(t, 0, ensurer.calls)
}

// The fetch decrypts the monitoring password and keeps the Coolify
// container credentials for the Docker path.
func TestGetCreds_DecryptsTheMonitoringPasswordAndKeepsContainerCreds(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").Return([]credential.Credential{
		{Dialect: "postgresql", PasswdEnc: "enc-customer", ContainerName: "dbuuid1", MonitorUsername: "selfhost_monitor", MonitorPasswdEnc: "enc-monitor"},
	}, nil)
	mocks.crypto.On("LoadPrivateKey", "/test/key/path").Return(testPrivateKey, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "enc-customer", testPrivateKey).Return("customer-pw", nil)
	mocks.crypto.On("DecryptWithPrivateKey", "enc-monitor", testPrivateKey).Return("monitor-pw", nil)

	creds, err := mp.GetCreds()

	require.NoError(t, err)
	assert.Equal(t, "monitor-pw", *creds[0].MonitorPassword)
	got, ok := mp.containerCred("dbuuid1")
	require.True(t, ok)
	assert.Equal(t, "selfhost_monitor", got.MonitorUsername)
}

// A monitoring password that cannot be decrypted never fails the fetch: the
// container is still the platform's, on the customer login it supplies.
func TestGetCreds_AnUndecryptableMonitoringPasswordFallsBackToThePlatformLogin(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").Return([]credential.Credential{
		{Dialect: "postgresql", Source: "coolify", PasswdEnc: "enc-customer", ContainerName: "dbuuid1", MonitorUsername: "selfhost_monitor", MonitorPasswdEnc: "garbage"},
	}, nil)
	mocks.crypto.On("LoadPrivateKey", "/test/key/path").Return(testPrivateKey, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "enc-customer", testPrivateKey).Return("customer-pw", nil)
	mocks.crypto.On("DecryptWithPrivateKey", "garbage", testPrivateKey).Return("", errors.New("bad ciphertext"))

	creds, err := mp.GetCreds()

	require.NoError(t, err)
	assert.Nil(t, creds[0].MonitorPassword)
	got, ok := mp.containerCred("dbuuid1")
	require.True(t, ok)
	assert.Equal(t, "customer-pw", *got.Password)
}

// One Coolify database's undecryptable credential never stops the others,
// nor the fetch.
func TestGetCreds_AnUndecryptableCoolifyCredentialIsSkippedNotFatal(t *testing.T) {
	mp, mocks := setupTestMetricsPusher()
	mocks.agentstate.On("GetAgentID").Return("agent-123")
	mocks.apiserver.On("GetMetricsCreds", mock.Anything, "agent-123").Return([]credential.Credential{
		{ID: "db1", Dialect: "postgresql", Source: "coolify", ContainerName: "dbuuid1", PasswdEnc: "garbage"},
		{ID: "db2", Dialect: "postgresql", Source: "coolify", ContainerName: "dbuuid2", PasswdEnc: "enc-2"},
	}, nil)
	mocks.crypto.On("LoadPrivateKey", "/test/key/path").Return(testPrivateKey, nil)
	mocks.crypto.On("DecryptWithPrivateKey", "garbage", testPrivateKey).Return("", errors.New("bad ciphertext"))
	mocks.crypto.On("DecryptWithPrivateKey", "enc-2", testPrivateKey).Return("pw-2", nil)

	creds, err := mp.GetCreds()

	require.NoError(t, err)
	assert.Nil(t, creds[0].Password)
	assert.Equal(t, "pw-2", *creds[1].Password)
}
