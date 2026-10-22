package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/storagemetrics"
)

// A scripted engine collector: one per container, results per call.
type scriptedEngine struct {
	creds   []credential.Credential
	results []any
	errs    []error
}

func (s *scriptedEngine) collect(cred credential.Credential) (any, error) {
	i := len(s.creds)
	s.creds = append(s.creds, cred)
	if i >= len(s.errs) {
		i = len(s.errs) - 1
	}
	return s.results[i], s.errs[i]
}

type ensureCall struct {
	dbType      dockerdiscovery.DatabaseType
	admin       credential.Credential
	user, pw    string
	hadDeadline bool
}

func engineHost(t *testing.T, dbs []dockerdiscovery.DiscoveredDatabase, engines map[string]*scriptedEngine, ensureErr error) (*metricspusher, *[]ensureCall, *domainmetrics.MetricPayload) {
	t.Helper()
	mp, mocks := setupTestMetricsPusher()
	mocks.agentstate.On("GetAgentID").Return("agent-123")
	setupSysCollectorMocks(mocks.syscollector)
	setupNetCollectorMocks(mocks.netcollector)
	mocks.storagecollector.On("Collect", mock.Anything).Return([]storagemetrics.StorageMetricSet(nil), nil)
	mocks.dockerDiscoverer.ExpectedCalls = nil
	mocks.dockerDiscoverer.On("DiscoverDatabases", mock.Anything).Return(dbs, nil)

	mp.newEngineCollector = func(containerID string, _ dockerdiscovery.DatabaseType) engineCollectFunc {
		return engines[containerID].collect
	}
	calls := &[]ensureCall{}
	mp.engineEnsurer = func(ctx context.Context, dbType dockerdiscovery.DatabaseType, admin credential.Credential, user, pw string) error {
		_, dl := ctx.Deadline()
		*calls = append(*calls, ensureCall{dbType, admin, user, pw, dl})
		return ensureErr
	}
	var pushed domainmetrics.MetricPayload
	mocks.apiserver.On("PushMetrics", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		pushed = args.Get(1).(domainmetrics.MetricPayload)
	}).Return(nil)
	return mp, calls, &pushed
}

func engineDB(kind dockerdiscovery.DatabaseType, name string, port uint16) dockerdiscovery.DiscoveredDatabase {
	return dockerdiscovery.DiscoveredDatabase{
		Type: kind, ContainerID: name + "000000000000", ContainerName: name,
		Host: "10.0.1.7", Port: port, Username: "envuser", Password: "envpw", Database: "appdb",
	}
}

func engineCred(dialect, container, customerPW, monitorPW, adminPW string) credential.Credential {
	c := credential.Credential{Dialect: dialect, Source: "coolify", ContainerName: container, Username: "appuser", Database: "appdb"}
	if customerPW != "" {
		c.Password = &customerPW
	}
	if monitorPW != "" {
		c.MonitorUsername, c.MonitorPassword = "selfhost_monitor", &monitorPW
	}
	if adminPW != "" {
		c.AdminUsername, c.AdminPassword = "root", &adminPW
	}
	return c
}

func engineSet(t *testing.T, p *domainmetrics.MetricPayload, typ, container string) map[string]any {
	t.Helper()
	for _, ms := range p.MetricSets {
		if ms.Type == typ && ms.Attributes["container_name"] == container {
			assert.Equal(t, "docker", ms.Attributes["source"])
			body, _ := json.Marshal(ms.Metrics)
			var m map[string]any
			require.NoError(t, json.Unmarshal(body, &m))
			return m
		}
	}
	t.Fatalf("no %s set for %s", typ, container)
	return nil
}

var refused = fmt.Errorf("ping: %w", &net.OpError{Op: "dial", Err: errnoOf(syscall.ECONNREFUSED)})

type errnoErr struct{ errno syscall.Errno }

func (e *errnoErr) Error() string   { return e.errno.Error() }
func (e *errnoErr) Unwrap() error   { return e.errno }
func errnoOf(e syscall.Errno) error { return &errnoErr{e} }

// MySQL: monitored as selfhost_monitor (no database: the user has no
// per-database grants), at the container's own address, healthy payload
// carrying the collector's numbers.
func TestEngine_MySQLCollectsAsTheMonitoringUser(t *testing.T) {
	my := &scriptedEngine{results: []any{domainmetrics.MySQLDatabaseMetrics{ConnectionsTotal: 4, MaxConnections: 151}}, errs: []error{nil}}
	mp, calls, pushed := engineHost(t, []dockerdiscovery.DiscoveredDatabase{engineDB(dockerdiscovery.DatabaseTypeMySQL, "mysqluuid", 3306)},
		map[string]*scriptedEngine{"mysqluuid000000000000": my}, nil)
	mp.setContainerCreds([]credential.Credential{engineCred("mysql", "mysqluuid", "app-pw", "mon-pw", "root-pw")})

	require.NoError(t, mp.Push(credential.Credential{}))

	assert.Equal(t, "selfhost_monitor", my.creds[0].Username)
	assert.Equal(t, "mon-pw", *my.creds[0].Password)
	assert.Equal(t, "", my.creds[0].Database)
	assert.Equal(t, "10.0.1.7", my.creds[0].Host)
	assert.Empty(t, *calls)
	m := engineSet(t, pushed, domainmetrics.MetricTypeMySQLDatabase, "mysqluuid")
	assert.Equal(t, true, m["up"])
	assert.Equal(t, "healthy", m["health"])
	assert.Equal(t, "complete", m["collection_status"])
	assert.Equal(t, float64(4), m["connections_total"])
}

// The monitoring user does not exist yet (an existing database) or was
// changed: created as the platform's admin at the container's address, then
// collected again.
func TestEngine_MySQLCreatesTheMonitoringUserOnAFailedLogin(t *testing.T) {
	denied := &mysql.MySQLError{Number: 1045, Message: "Access denied"}
	my := &scriptedEngine{results: []any{nil, domainmetrics.MySQLDatabaseMetrics{MaxConnections: 151}}, errs: []error{denied, nil}}
	mp, calls, pushed := engineHost(t, []dockerdiscovery.DiscoveredDatabase{engineDB(dockerdiscovery.DatabaseTypeMySQL, "mysqluuid", 3306)},
		map[string]*scriptedEngine{"mysqluuid000000000000": my}, nil)
	mp.setContainerCreds([]credential.Credential{engineCred("mysql", "mysqluuid", "app-pw", "mon-pw", "root-pw")})

	require.NoError(t, mp.Push(credential.Credential{}))

	require.Len(t, *calls, 1)
	c := (*calls)[0]
	assert.Equal(t, dockerdiscovery.DatabaseTypeMySQL, c.dbType)
	assert.Equal(t, "root", c.admin.Username)
	assert.Equal(t, "root-pw", *c.admin.Password)
	assert.Equal(t, "10.0.1.7", c.admin.Host)
	assert.Equal(t, 3306, c.admin.Port)
	assert.Equal(t, "selfhost_monitor", c.user)
	assert.Equal(t, "mon-pw", c.pw)
	assert.True(t, c.hadDeadline, "a repair is bounded")
	assert.Len(t, my.creds, 2)
	assert.Equal(t, "healthy", engineSet(t, pushed, domainmetrics.MetricTypeMySQLDatabase, "mysqluuid")["health"])
}

// The admin password no longer works either (the customer changed root):
// collected on the platform's customer login instead of auth_failed for good.
func TestEngine_AFailedRepairFallsBackToThePlatformLogin(t *testing.T) {
	denied := &mysql.MySQLError{Number: 1045}
	my := &scriptedEngine{results: []any{nil, domainmetrics.MySQLDatabaseMetrics{MaxConnections: 151}}, errs: []error{denied, nil}}
	mp, calls, pushed := engineHost(t, []dockerdiscovery.DiscoveredDatabase{engineDB(dockerdiscovery.DatabaseTypeMySQL, "mysqluuid", 3306)},
		map[string]*scriptedEngine{"mysqluuid000000000000": my}, errors.New("admin login: Access denied for user 'root'"))
	mp.setContainerCreds([]credential.Credential{engineCred("mysql", "mysqluuid", "app-pw", "mon-pw", "root-pw")})

	require.NoError(t, mp.Push(credential.Credential{}))

	assert.Len(t, *calls, 1)
	require.Len(t, my.creds, 2)
	assert.Equal(t, "appuser", my.creds[1].Username)
	assert.Equal(t, "app-pw", *my.creds[1].Password)
	assert.Equal(t, "healthy", engineSet(t, pushed, domainmetrics.MetricTypeMySQLDatabase, "mysqluuid")["health"])
}

// MongoDB: the same, with the platform's root as admin.
func TestEngine_MongoCreatesItsMonitoringUser(t *testing.T) {
	authErr := errors.New("connection() error occurred during connection handshake: auth error: (AuthenticationFailed) Authentication failed.")
	mg := &scriptedEngine{results: []any{nil, domainmetrics.MongoDBMetrics{ConnectionsCurrent: 2}}, errs: []error{authErr, nil}}
	mp, calls, _ := engineHost(t, []dockerdiscovery.DiscoveredDatabase{engineDB(dockerdiscovery.DatabaseTypeMongoDB, "mongouuid", 27017)},
		map[string]*scriptedEngine{"mongouuid000000000000": mg}, nil)
	mp.setContainerCreds([]credential.Credential{engineCred("mongodb", "mongouuid", "root-pw", "mon-pw", "root-pw")})

	require.NoError(t, mp.Push(credential.Credential{}))

	require.Len(t, *calls, 1)
	assert.Equal(t, dockerdiscovery.DatabaseTypeMongoDB, (*calls)[0].dbType)
	assert.Equal(t, 27017, (*calls)[0].admin.Port)
	assert.Equal(t, "selfhost_monitor", mg.creds[1].Username)
}

// Redis: the platform's password, never the container's env; no monitoring
// user. A rejected password is unknown, with no numbers, never zeros.
func TestEngine_RedisUsesThePlatformPasswordAndReportsAuthHonestly(t *testing.T) {
	rd := &scriptedEngine{results: []any{nil}, errs: []error{errors.New("WRONGPASS invalid username-password pair or user is disabled.")}}
	mp, calls, pushed := engineHost(t, []dockerdiscovery.DiscoveredDatabase{engineDB(dockerdiscovery.DatabaseTypeRedis, "redisuuid", 6379)},
		map[string]*scriptedEngine{"redisuuid000000000000": rd}, nil)
	c := engineCred("redis", "redisuuid", "platform-redis-pw", "", "")
	c.Username = ""
	mp.setContainerCreds([]credential.Credential{c})

	require.NoError(t, mp.Push(credential.Credential{}))

	assert.Equal(t, "platform-redis-pw", *rd.creds[0].Password)
	assert.Empty(t, *calls, "Redis gets no monitoring user")
	m := engineSet(t, pushed, domainmetrics.MetricTypeRedis, "redisuuid")
	assert.Nil(t, m["up"])
	assert.Equal(t, "unknown", m["health"])
	assert.Equal(t, "auth_failed", m["collection_error"])
	assert.NotContains(t, m, "connected_clients", "unread numbers are absent, never 0")
}

// Refused: down, with no numbers.
func TestEngine_RefusedIsUnhealthy(t *testing.T) {
	mg := &scriptedEngine{results: []any{nil}, errs: []error{refused}}
	mp, _, pushed := engineHost(t, []dockerdiscovery.DiscoveredDatabase{engineDB(dockerdiscovery.DatabaseTypeMongoDB, "mongouuid", 27017)},
		map[string]*scriptedEngine{"mongouuid000000000000": mg}, nil)

	require.NoError(t, mp.Push(credential.Credential{}))

	m := engineSet(t, pushed, domainmetrics.MetricTypeMongoDBDatabase, "mongouuid")
	assert.Equal(t, false, m["up"])
	assert.Equal(t, "unhealthy", m["health"])
	assert.Equal(t, "connection_refused", m["collection_error"])
	assert.NotContains(t, m, "connections_current")
}

// A platform database with no usable password anywhere: missing_credentials,
// no login attempted.
func TestEngine_MissingCredentialsAreExplicit(t *testing.T) {
	db := engineDB(dockerdiscovery.DatabaseTypeMySQL, "mysqluuid", 3306)
	db.Password = ""
	my := &scriptedEngine{results: []any{nil}, errs: []error{nil}}
	mp, _, pushed := engineHost(t, []dockerdiscovery.DiscoveredDatabase{db}, map[string]*scriptedEngine{"mysqluuid000000000000": my}, nil)
	mp.setContainerCreds([]credential.Credential{engineCred("mysql", "mysqluuid", "", "", "")})

	require.NoError(t, mp.Push(credential.Credential{}))

	assert.Empty(t, my.creds)
	assert.Equal(t, "missing_credentials", engineSet(t, pushed, domainmetrics.MetricTypeMySQLDatabase, "mysqluuid")["collection_error"])
}

// A container the platform does not name keeps its env login, and nothing
// is created in it.
func TestEngine_UnnamedContainerUsesItsEnvLogin(t *testing.T) {
	my := &scriptedEngine{results: []any{nil}, errs: []error{&mysql.MySQLError{Number: 1045}}}
	mp, calls, _ := engineHost(t, []dockerdiscovery.DiscoveredDatabase{engineDB(dockerdiscovery.DatabaseTypeMySQL, "template-mysql", 3306)},
		map[string]*scriptedEngine{"template-mysql000000000000": my}, nil)
	mp.setContainerCreds([]credential.Credential{engineCred("mysql", "otheruuid", "app-pw", "mon-pw", "root-pw")})

	require.NoError(t, mp.Push(credential.Credential{}))

	assert.Equal(t, "envuser", my.creds[0].Username)
	assert.Empty(t, *calls)
}

// No address known: unknown / no_address, nothing dialled.
func TestEngine_NoAddressIsUnknownWithoutDialling(t *testing.T) {
	db := engineDB(dockerdiscovery.DatabaseTypeRedis, "redisuuid", 6379)
	db.Host = ""
	rd := &scriptedEngine{results: []any{nil}, errs: []error{nil}}
	mp, _, pushed := engineHost(t, []dockerdiscovery.DiscoveredDatabase{db}, map[string]*scriptedEngine{"redisuuid000000000000": rd}, nil)

	require.NoError(t, mp.Push(credential.Credential{}))

	assert.Empty(t, rd.creds)
	assert.Equal(t, "no_address", engineSet(t, pushed, domainmetrics.MetricTypeRedis, "redisuuid")["collection_error"])
}

// A repair that keeps failing is not rerun every push.
func TestEngine_RepairIsRateLimited(t *testing.T) {
	my := &scriptedEngine{results: []any{nil}, errs: []error{&mysql.MySQLError{Number: 1045}}}
	mp, calls, _ := engineHost(t, []dockerdiscovery.DiscoveredDatabase{engineDB(dockerdiscovery.DatabaseTypeMySQL, "mysqluuid", 3306)},
		map[string]*scriptedEngine{"mysqluuid000000000000": my}, errors.New("still refused"))
	mp.setContainerCreds([]credential.Credential{engineCred("mysql", "mysqluuid", "", "mon-pw", "root-pw")})

	for i := 0; i < 3; i++ {
		require.NoError(t, mp.Push(credential.Credential{}))
	}

	assert.Len(t, *calls, 1)
}
