package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"hostlink/domain/credential"
	domainmetrics "hostlink/domain/metrics"
	"hostlink/internal/dockerdiscovery"
	"hostlink/internal/pgmetrics"
)

// ── acceptance: every container, matched by uuid ────────────────────────────

func platformCred(container, customerPW, monitorPW string) credential.Credential {
	c := credential.Credential{Dialect: "postgresql", Source: "coolify", Host: "localhost", Port: 5433,
		ContainerName: container, Username: "customer", Database: "appdb"}
	if customerPW != "" {
		c.Password = &customerPW
	}
	if monitorPW != "" {
		c.MonitorUsername, c.MonitorPassword = "selfhost_monitor", &monitorPW
	}
	return c
}

// Several databases on one project: each container gets its own platform
// login, by uuid, whatever order the platform lists them in. The
// credential's localhost and public port (5433) are never dialled: each
// container is reached at its own address, so a private database works with
// no public port.
func TestPush_EveryCoolifyContainerIsMatchedByUUIDNotListPosition(t *testing.T) {
	dbs := []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1"), coolifyPG("dbuuid2"), coolifyPG("dbuuid3")}
	dbs[1].Host = "10.0.1.8"
	dbs[2].Host = "10.0.1.9"
	mp, _, _, pushed := setupCoolifyHost(t, dbs)
	collectors := map[string]*scriptedPG{}
	mp.newPGContainer = func(id string) pgContainerCollector {
		pg := &scriptedPG{results: []domainmetrics.PostgreSQLContainerMetrics{healthy}, fails: []*pgmetrics.Failure{nil}}
		collectors[id] = pg
		return pg
	}
	mp.setContainerCreds([]credential.Credential{
		platformCred("dbuuid3", "c3", "m3"), platformCred("dbuuid1", "c1", "m1"), platformCred("dbuuid2", "c2", "m2"),
	})

	assert.NoError(t, mp.Push(credential.Credential{}))

	for i, want := range map[string]struct{ host, pw string }{
		"dbuuid1000000000000": {"10.0.1.7", "m1"}, "dbuuid2000000000000": {"10.0.1.8", "m2"}, "dbuuid3000000000000": {"10.0.1.9", "m3"},
	} {
		got := collectors[i].creds[0]
		assert.Equal(t, want.host, got.Host, i)
		assert.Equal(t, 5432, got.Port, "%s: the container port, not the public one", i)
		assert.Equal(t, want.pw, *got.Password, i)
	}
	for _, name := range []string{"dbuuid1", "dbuuid2", "dbuuid3"} {
		pgSet(t, pushed, name)
	}
}

// No monitoring login yet: the platform's record of the customer's password,
// never the container's env, which is stale once the password was changed.
func TestPush_StaleEnvPasswordIsNeverUsedForAPlatformDatabase(t *testing.T) {
	mp, _, _, _ := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")}) // env password "pw"
	pg := &scriptedPG{results: []domainmetrics.PostgreSQLContainerMetrics{healthy}, fails: []*pgmetrics.Failure{nil}}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	mp.setContainerCreds([]credential.Credential{platformCred("dbuuid1", "fresh-pw", "")})

	assert.NoError(t, mp.Push(credential.Credential{}))

	assert.Equal(t, "customer", pg.creds[0].Username)
	assert.Equal(t, "fresh-pw", *pg.creds[0].Password)
}

// Missing or invalid credentials: an explicit collection error, never a
// login attempt that reads as zero metrics.
func TestPush_MissingCredentialsAreAnExplicitError(t *testing.T) {
	db := coolifyPG("dbuuid1")
	db.Password = ""
	mp, _, _, pushed := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{db})
	pg := &scriptedPG{results: []domainmetrics.PostgreSQLContainerMetrics{healthy}, fails: []*pgmetrics.Failure{nil}}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	mp.setContainerCreds([]credential.Credential{platformCred("dbuuid1", "", "")}) // nothing decryptable

	assert.NoError(t, mp.Push(credential.Credential{}))

	assert.Empty(t, pg.creds, "no login attempted")
	body, _ := json.Marshal(pgSet(t, pushed, "dbuuid1").Metrics)
	assert.Contains(t, string(body), `"collection_error":"missing_credentials"`)
	assert.Contains(t, string(body), `"up":null`)
	assert.Contains(t, string(body), `"connections_total":null`)
}

// A role refused CONNECT (REVOKE CONNECT ... FROM PUBLIC) is repaired too.
func TestPush_ARoleRefusedConnectIsRepaired(t *testing.T) {
	mp, _, _, _ := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")})
	denied := &pgmetrics.Failure{Stage: "connect", Kind: "permission_denied", Err: errors.New("pq: permission denied for database appdb")}
	pg := &scriptedPG{
		results: []domainmetrics.PostgreSQLContainerMetrics{authFailed, healthy},
		fails:   []*pgmetrics.Failure{denied, nil},
	}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	ensurer := &fakeRoleEnsurer{}
	mp.roleEnsurer = ensurer
	mp.setContainerCreds([]credential.Credential{monitorCred("dbuuid1")})

	assert.NoError(t, mp.Push(credential.Credential{}))

	assert.Equal(t, 1, ensurer.calls)
	assert.Len(t, pg.creds, 2)
}

// The platform replaced the monitoring password: repaired at once, not after
// the rate-limit window.
func TestPush_AChangedMonitoringPasswordIsRepairedAtOnce(t *testing.T) {
	mp, _, _, _ := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")})
	pg := &scriptedPG{results: []domainmetrics.PostgreSQLContainerMetrics{authFailed}, fails: []*pgmetrics.Failure{authErr}}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	ensurer := &fakeRoleEnsurer{}
	mp.roleEnsurer = ensurer
	mp.setContainerCreds([]credential.Credential{monitorCred("dbuuid1")})
	assert.NoError(t, mp.Push(credential.Credential{}))

	mp.setContainerCreds([]credential.Credential{platformCred("dbuuid1", "c", "new-monitor-pw")})
	assert.NoError(t, mp.Push(credential.Credential{}))

	assert.Equal(t, 2, ensurer.calls)
	assert.Equal(t, "new-monitor-pw", ensurer.password)
}

// A repair is bounded: it gets a deadline.
func TestPush_ARepairHasADeadline(t *testing.T) {
	mp, _, _, _ := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")})
	pg := &scriptedPG{results: []domainmetrics.PostgreSQLContainerMetrics{authFailed}, fails: []*pgmetrics.Failure{authErr}}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	ensurer := &deadlineEnsurer{}
	mp.roleEnsurer = ensurer
	mp.setContainerCreds([]credential.Credential{monitorCred("dbuuid1")})

	assert.NoError(t, mp.Push(credential.Credential{}))

	assert.True(t, ensurer.hadDeadline)
}

type deadlineEnsurer struct{ hadDeadline bool }

func (d *deadlineEnsurer) Ensure(ctx context.Context, _, _, _, _, _ string) error {
	_, d.hadDeadline = ctx.Deadline()
	return errors.New("still refused")
}

// The role cannot be created (an image whose local socket psql does not find,
// a renamed superuser): collect with the platform's record of the customer's
// password rather than report auth_failed for good.
func TestPush_ARoleThatCannotBeCreatedFallsBackToThePlatformLogin(t *testing.T) {
	mp, _, _, pushed := setupCoolifyHost(t, []dockerdiscovery.DiscoveredDatabase{coolifyPG("dbuuid1")})
	pg := &scriptedPG{
		results: []domainmetrics.PostgreSQLContainerMetrics{authFailed, healthy},
		fails:   []*pgmetrics.Failure{authErr, nil},
	}
	mp.newPGContainer = func(string) pgContainerCollector { return pg }
	mp.roleEnsurer = &fakeRoleEnsurer{err: errors.New("psql: could not connect to socket")}
	mp.setContainerCreds([]credential.Credential{platformCred("dbuuid1", "customer-pw", "monitor-pw")})

	assert.NoError(t, mp.Push(credential.Credential{}))

	if assert.Len(t, pg.creds, 2) {
		assert.Equal(t, "selfhost_monitor", pg.creds[0].Username)
		assert.Equal(t, "customer", pg.creds[1].Username)
		assert.Equal(t, "customer-pw", *pg.creds[1].Password)
	}
	body, _ := json.Marshal(pgSet(t, pushed, "dbuuid1").Metrics)
	assert.Contains(t, string(body), `"health":"healthy"`)
}
