package kafkametrics

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hostlink/domain/credential"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
)

// The issue's "good test" (selfhost#2854), against an in-memory Kafka cluster:
// a group deliberately stopped mid-topic shows its lag; caught up, it reads 0;
// no groups at all reads 0 groups / 0 lag — and every one of those is a REAL
// reading, distinguishable from "unknown".
func TestGroupLags_AgainstAnInMemoryCluster(t *testing.T) {
	cluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, "orders"))
	require.NoError(t, err)
	defer cluster.Close()

	cl, err := kgo.NewClient(kgo.SeedBrokers(cluster.ListenAddrs()...))
	require.NoError(t, err)
	defer cl.Close()
	adm := kadm.NewClient(cl)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := 0; i < 20; i++ {
		require.NoError(t, cl.ProduceSync(ctx, &kgo.Record{Topic: "orders", Value: []byte("x")}).FirstErr())
	}

	snap, err := groupLags(ctx, adm)
	require.NoError(t, err)
	assert.Equal(t, GroupLagSnapshot{Groups: 0, MaxLag: 0, Complete: true}, snap, "no groups: a real, complete zero — not unknown")

	commit := func(group string, offset int64) {
		var offs kadm.Offsets
		offs.AddOffset("orders", 0, offset, -1)
		require.NoError(t, adm.CommitAllOffsets(ctx, group, offs))
	}
	commit("billing", 5)    // stopped mid-topic: 20 - 5 = 15 behind
	commit("analytics", 20) // caught up

	snap, err = groupLags(ctx, adm)
	require.NoError(t, err)
	assert.Equal(t, 2, snap.Groups)
	assert.True(t, snap.Complete)
	assert.Equal(t, int64(15), snap.MaxLag, "the worst group's total lag is what the platform key carries")

	commit("billing", 20) // the stuck consumer catches up
	snap, err = groupLags(ctx, adm)
	require.NoError(t, err)
	assert.Equal(t, int64(0), snap.MaxLag, "all caught up → back to 0")
	assert.Equal(t, 2, snap.Groups)
	assert.True(t, snap.Complete)
}

// The skip policy at its pure seam: a group that cannot be described, whose
// offsets cannot be fetched, whose end offset for any partition is unresolvable,
// or that kadm did not return at all is SKIPPED and the snapshot marked
// incomplete — the group count still stands (the list succeeded), the max lag is
// computed over the measured groups only and omitted downstream.
func TestAggregateLags_SkipsUnmeasurableGroupsAndMarksTheSnapshotIncomplete(t *testing.T) {
	lags := kadm.DescribedGroupLags{
		"billing": {Group: "billing", Lag: kadm.GroupLag{"orders": {0: {Lag: 6}, 1: {Lag: -3}}}}, // negative lag never counts
		"ghost":   {Group: "ghost", DescribeErr: errors.New("coordinator not available")},
		"stale":   {Group: "stale", FetchErr: errors.New("offset fetch failed")},
		"torn":    {Group: "torn", Lag: kadm.GroupLag{"tmp": {0: {Lag: 99, Err: errors.New("UNKNOWN_TOPIC_OR_PARTITION")}}}},
	}

	snap := aggregateLags([]string{"billing", "ghost", "stale", "torn", "vanished"}, lags)

	assert.Equal(t, 5, snap.Groups, "the group count is still a real reading")
	assert.False(t, snap.Complete)
	assert.Equal(t, []string{"ghost", "stale", "torn", "vanished"}, snap.Skipped)
	assert.Equal(t, int64(6), snap.MaxLag, "torn's 99 must NOT leak in — its offsets are unresolved")

	complete := aggregateLags([]string{"billing"}, lags)
	assert.True(t, complete.Complete)
	assert.Nil(t, complete.Skipped)
	assert.Equal(t, int64(6), complete.MaxLag)
}

func TestReadProperties_AcceptsJavaPropertySyntax(t *testing.T) {
	props, err := readProperties(writeProps(t, "# comment\n! bang comment\nprocess.roles=broker,controller\nlisteners = CLIENT://0.0.0.0:9092,INTERNAL://127.0.0.1:9094\nadvertised.listeners:CLIENT://x:9092,INTERNAL://127.0.0.1:9094\nlisteners=PLAINTEXT://0.0.0.0:9092\n"))
	require.NoError(t, err)
	assert.Equal(t, "PLAINTEXT://0.0.0.0:9092", props["listeners"], "last key wins, like Kafka's loader")
	assert.Equal(t, "CLIENT://x:9092,INTERNAL://127.0.0.1:9094", props["advertised.listeners"], "`key:value` form")
	assert.Equal(t, "broker,controller", props["process.roles"])
}

// The routing hazard: kgo dials brokers at their ADVERTISED address after the
// first Metadata. This node's own advertised address for the chosen listener
// must come back to localhost; peers must not.
func TestDiscoverBootstrap_RewritesOwnAdvertisedAddressToLocalhost(t *testing.T) {
	cases := []struct {
		name, props, wantAddr string
		wantRewrites          map[string]string
	}{
		{
			"legacy single PLAINTEXT advertises the public DNS",
			"listeners=PLAINTEXT://0.0.0.0:9092,CONTROLLER://127.0.0.1:9093\nadvertised.listeners=PLAINTEXT://kafka-abc.example.com:9092\n",
			"127.0.0.1:9092", map[string]string{"kafka-abc.example.com:9092": "127.0.0.1:9092"},
		},
		{
			"SASL HA node advertises INTERNAL on its private IP",
			"listeners=CLIENT://0.0.0.0:9092,INTERNAL://0.0.0.0:9094,CONTROLLER://0.0.0.0:9093\nadvertised.listeners=CLIENT://kafka-abc.example.com:9092,INTERNAL://10.0.1.10:9094\n",
			"127.0.0.1:9094", map[string]string{"10.0.1.10:9094": "127.0.0.1:9094"},
		},
		{
			"SASL single node already advertises INTERNAL on localhost",
			"listeners=CLIENT://0.0.0.0:9092,INTERNAL://127.0.0.1:9094,CONTROLLER://127.0.0.1:9093\nadvertised.listeners=CLIENT://kafka-abc.example.com:9092,INTERNAL://127.0.0.1:9094\n",
			"127.0.0.1:9094", map[string]string{"127.0.0.1:9094": "127.0.0.1:9094"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			boot, err := discoverBootstrap(writeProps(t, tc.props))
			require.NoError(t, err)
			assert.Equal(t, tc.wantAddr, boot.addr)
			assert.Equal(t, tc.wantRewrites, boot.rewrites, "the CLIENT (SASL/TLS) advertised address is never rewritten or dialled")
		})
	}
}

func TestLocalRewritingDialer_RedirectsOnlyTheRewrittenAddress(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()

	dial := localRewritingDialer(map[string]string{"kafka-abc.example.com:9092": ln.Addr().String()})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := dial(ctx, "tcp", "kafka-abc.example.com:9092")
	require.NoError(t, err, "this node's own advertised address is served from localhost")
	conn.Close()

	_, err = dial(ctx, "tcp", "127.0.0.1:1")
	require.Error(t, err, "an address without a rewrite is dialled as given")
}

func TestAdminLagSource_UnreachableBrokerIsUnknownNotZero(t *testing.T) {
	props := writeProps(t, "listeners=INTERNAL://127.0.0.1:1,CONTROLLER://127.0.0.1:2\n")
	src := newAdminLagSource(props)

	_, err := src.GroupLags(context.Background())
	require.Error(t, err, "nothing listens on port 1 — the snapshot must be unknown")
	assert.Nil(t, src.admin, "a failed query drops the client so the next heartbeat reconnects")
}

func TestAdminLagSource_MissingServerPropertiesIsUnknown(t *testing.T) {
	src := newAdminLagSource(filepath.Join(t.TempDir(), "absent.properties"))
	_, err := src.GroupLags(context.Background())
	require.Error(t, err)
}

func TestBootstrapFromListeners(t *testing.T) {
	cases := []struct {
		name, listeners, want string
		wantErr               bool
	}{
		{"SASL single node prefers INTERNAL over CLIENT", "CLIENT://0.0.0.0:9092,INTERNAL://127.0.0.1:9094,CONTROLLER://127.0.0.1:9093", "127.0.0.1:9094", false},
		{"HA node binds 0.0.0.0 but is dialled on localhost", "CLIENT://0.0.0.0:9092,INTERNAL://0.0.0.0:9094,CONTROLLER://0.0.0.0:9093", "127.0.0.1:9094", false},
		{"legacy anonymous PLAINTEXT listener", "PLAINTEXT://0.0.0.0:9092,CONTROLLER://127.0.0.1:9093", "127.0.0.1:9092", false},
		{"CLIENT alone is never used (SASL/TLS)", "CLIENT://0.0.0.0:9092,CONTROLLER://127.0.0.1:9093", "", true},
		{"garbage", "not-a-listener", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := bootstrapFromListeners(tc.listeners)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestDiscoverBootstrap_ReadsListenersLine(t *testing.T) {
	props := writeProps(t, "# Managed by selfhost\nprocess.roles=broker,controller\nlisteners=CLIENT://0.0.0.0:9092,INTERNAL://127.0.0.1:9094,CONTROLLER://127.0.0.1:9093\nadvertised.listeners=CLIENT://x:9092\n")
	boot, err := discoverBootstrap(props)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1:9094", boot.addr)
	assert.Empty(t, boot.rewrites, "no INTERNAL advertised address → nothing to rewrite; CLIENT is never touched")
}

// ── collector integration with a fake source: the JSON contract ──────────────

type fakeLag struct {
	snap GroupLagSnapshot
	err  error
}

func (f fakeLag) GroupLags(context.Context) (GroupLagSnapshot, error) { return f.snap, f.err }

func TestCollect_ReportsLagWithProvenanceOrOmitsIt(t *testing.T) {
	srv, setBody := mutableMetricsServer(t)
	defer srv.Close()
	setBody(dump(1, 1, 1, 1, 1, 1, 1))

	t.Run("computed lag is emitted with its source, a real 0 included", func(t *testing.T) {
		c := NewWithSources(srv.URL, fakeLag{snap: GroupLagSnapshot{Groups: 3, MaxLag: 0, Complete: true}})
		m, err := c.Collect(credential.Credential{})
		require.NoError(t, err)
		body, _ := json.Marshal(m)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		assert.Equal(t, float64(0), out["max_consumer_group_lag"], "a computed 0 is a real reading and must be sent")
		assert.Equal(t, float64(3), out["consumer_group_count"])
		assert.Equal(t, "admin_api", out["consumer_lag_source"])
	})

	t.Run("a partial measurement reports the group count but omits the max lag", func(t *testing.T) {
		c := NewWithSources(srv.URL, fakeLag{snap: GroupLagSnapshot{Groups: 3, MaxLag: 40, Complete: false, Skipped: []string{"ghost"}}})
		m, err := c.Collect(credential.Credential{})
		require.NoError(t, err)
		body, _ := json.Marshal(m)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		assert.Equal(t, float64(3), out["consumer_group_count"], "the list succeeded — a real reading")
		_, hasLag := out["max_consumer_group_lag"]
		assert.False(t, hasLag, "a max over a subset would under-state the worst group — omitted")
		assert.Equal(t, "admin_api", out["consumer_lag_source"])
	})

	t.Run("unknown lag omits the keys and the tag — never a fake 0", func(t *testing.T) {
		c := NewWithSources(srv.URL, fakeLag{err: errors.New("broker unreachable")})
		m, err := c.Collect(credential.Credential{})
		require.NoError(t, err)
		assert.True(t, m.Up, "the scrape still succeeded")
		body, _ := json.Marshal(m)
		var out map[string]any
		require.NoError(t, json.Unmarshal(body, &out))
		_, hasLag := out["max_consumer_group_lag"]
		_, hasCount := out["consumer_group_count"]
		_, hasSource := out["consumer_lag_source"]
		assert.False(t, hasLag, "absent, not zero")
		assert.False(t, hasCount)
		assert.False(t, hasSource)
	})
}

func writeProps(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "selfhost-server.properties")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o644))
	return p
}
