package kafkametrics

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Consumer-group lag via the Kafka admin API (selfhost#2854).
//
// AutoMQ's broker exporter has no consumer-group series at all: brokers do not
// compute lag (it is a client-side notion — committed offset vs log end), and the
// kafka_log_end_offset / kafka_group_commit_offset gauges an earlier version of
// this collector joined exist only in AutoMQ's docker telemetry stack, never on
// the broker's own /metrics. Joining them therefore produced a structural 0 for
// max_consumer_group_lag for ever — a stuck consumer looked healthy.
//
// This source asks the broker directly: ListGroups, then kadm.Lag (DescribeGroups
// + OffsetFetch + ListOffsets) over the localhost INTERNAL listener, which is
// PLAINTEXT with ANONYMOUS as a super user on every SASL instance (legacy
// instances have a single PLAINTEXT listener) — so no credentials, no TLS, and no
// cert hostname mismatch against 127.0.0.1. The listener port is read from the
// server.properties kafka_install/kafka_broker_config write.
//
// Semantics of the two platform keys:
//
//	consumer_group_count   — number of groups the broker lists (any state).
//	max_consumer_group_lag — the LARGEST per-group total lag (sum of that group's
//	                         positive partition lags), across all groups. The
//	                         platform key is one scalar; the worst group is what
//	                         pages.
//
// Completeness policy (one rule, both directions): anything that cannot be
// measured is OMITTED, never zeroed or under-stated. ListGroups failing → no
// snapshot → both keys omitted. A group whose description, committed offsets or
// any end offset could not be fetched → the group count is still reported (the
// list succeeded) but MaxLag is not — a max over a subset would UNDER-state the
// worst group, which is exactly the "looks healthy" failure this replaces.
//
// Routing: kgo uses the seed only for the first Metadata and then dials every
// broker at the address the broker ADVERTISES for the listener it was reached
// on. On a legacy (pre-SASL) instance that is the customer-facing public
// address, which the node cannot hairpin to through its own security group; on
// an HA node the INTERNAL address is its private IP. localRewritingDialer maps
// this node's own advertised host:port for the chosen listener back to
// 127.0.0.1:<port>; peers' addresses are dialled as advertised.

// defaultServerProperties is the file kafka_install.sh / kafka_broker_config.sh write.
const defaultServerProperties = "/opt/automq/kafka/config/kraft/selfhost-server.properties"

// ConsumerLagSourceAdminAPI tags a heartbeat whose consumer-group keys were really
// computed here. The control plane drops the two keys from any kafka.database set
// that lacks the tag (a legacy agent's structural zeros).
const ConsumerLagSourceAdminAPI = "admin_api"

// Collect runs the scrape and this query back to back inside one metrics push
// (every 20 s by default), so a hung broker delays the other metric sets by at
// most scrape + lag timeouts. Kept short: a few localhost round trips suffice.
const lagQueryTimeout = 5 * time.Second

// GroupLagSnapshot is one observation of the broker's consumer groups.
type GroupLagSnapshot struct {
	Groups int
	// MaxLag is the largest per-group total lag over the MEASURED groups. Only
	// meaningful when Complete; the collector omits it otherwise.
	MaxLag int64
	// Complete is false when any listed group could not be measured.
	Complete bool
	// Skipped names the groups that could not be measured (for the log line).
	Skipped []string
}

// LagSource computes a GroupLagSnapshot; an error means "unknown", never "zero".
type LagSource interface {
	GroupLags(ctx context.Context) (GroupLagSnapshot, error)
}

// adminLagSource is the production LagSource: a lazily-built, long-lived franz-go
// admin client against the broker's localhost INTERNAL/PLAINTEXT listener. Any
// query error drops the client so the next heartbeat reconnects cleanly (broker
// restart, listener change). Single caller assumed (one Collect per metrics
// push): reset() may Close a client another concurrent caller still holds.
type adminLagSource struct {
	propsPath string

	mu     sync.Mutex
	client *kgo.Client
	admin  *kadm.Client
}

func newAdminLagSource(propsPath string) *adminLagSource {
	return &adminLagSource{propsPath: propsPath}
}

func (a *adminLagSource) GroupLags(ctx context.Context) (GroupLagSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, lagQueryTimeout)
	defer cancel()

	adm, err := a.adminClient()
	if err != nil {
		return GroupLagSnapshot{}, err
	}
	snap, err := groupLags(ctx, adm)
	if err != nil {
		a.reset()
		return GroupLagSnapshot{}, err
	}
	return snap, nil
}

func (a *adminLagSource) adminClient() (*kadm.Client, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.admin != nil {
		return a.admin, nil
	}
	boot, err := discoverBootstrap(a.propsPath)
	if err != nil {
		return nil, err
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(boot.addr),
		kgo.ClientID("hostlink-kafkametrics"),
		kgo.Dialer(localRewritingDialer(boot.rewrites)),
		// KIP-714: franz-go would otherwise push ITS OWN client metrics into the
		// customer's broker (Kafka 3.9 advertises the telemetry APIs).
		kgo.DisableClientMetrics(),
	)
	if err != nil {
		return nil, fmt.Errorf("kafka admin client: %w", err)
	}
	a.client = cl
	a.admin = kadm.NewClient(cl)
	return a.admin, nil
}

func (a *adminLagSource) reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		a.client.Close()
	}
	a.client = nil
	a.admin = nil
}

// groupLags is the pure computation over an admin client (tested against kfake).
// See the completeness policy in the package comment: a group whose description
// or offsets could not be fetched, or any of whose partitions could not have its
// end offset listed, is SKIPPED and the snapshot is marked incomplete — the
// group count still stands (the list succeeded), the max lag does not.
func groupLags(ctx context.Context, adm *kadm.Client) (GroupLagSnapshot, error) {
	listed, err := adm.ListGroups(ctx)
	if err != nil {
		return GroupLagSnapshot{}, fmt.Errorf("list consumer groups: %w", err)
	}
	groups := listed.Groups()
	snap := GroupLagSnapshot{Groups: len(groups), Complete: true}
	if len(groups) == 0 {
		return snap, nil
	}

	lags, err := adm.Lag(ctx, groups...)
	if err != nil {
		return GroupLagSnapshot{}, fmt.Errorf("describe consumer group lag: %w", err)
	}
	return aggregateLags(groups, lags), nil
}

// aggregateLags folds kadm's per-group results into one snapshot: the max of
// the measurable groups' total lags, every unmeasurable group named in Skipped,
// Complete only when nothing was skipped. Pure, so the skip policy is testable
// without a broker that misbehaves on cue.
func aggregateLags(groups []string, lags kadm.DescribedGroupLags) GroupLagSnapshot {
	snap := GroupLagSnapshot{Groups: len(groups)}
	for _, g := range groups {
		l, ok := lags[g]
		if !ok || l.DescribeErr != nil || l.FetchErr != nil {
			snap.Skipped = append(snap.Skipped, g)
			continue
		}
		total, err := groupTotalLag(l.Lag)
		if err != nil {
			snap.Skipped = append(snap.Skipped, g)
			continue
		}
		if total > snap.MaxLag {
			snap.MaxLag = total
		}
	}
	snap.Complete = len(snap.Skipped) == 0
	return snap
}

// groupTotalLag sums a group's positive partition lags, failing on any partition
// whose offsets could not be resolved (kadm reports that per partition).
func groupTotalLag(l kadm.GroupLag) (int64, error) {
	var total int64
	for _, partitions := range l {
		for _, m := range partitions {
			if m.Err != nil {
				return 0, m.Err
			}
			if m.Lag > 0 {
				total += m.Lag
			}
		}
	}
	return total, nil
}

// bootstrap is where to dial the broker and how to keep every dial local.
type bootstrap struct {
	addr     string            // 127.0.0.1:<port> of the credential-free listener
	rewrites map[string]string // this node's advertised host:port for that listener → 127.0.0.1:<port>
}

// discoverBootstrap reads the broker's server.properties and returns the
// localhost address of a credential-free listener plus the dial rewrites that
// keep the client on localhost once it learns the advertised addresses.
func discoverBootstrap(propsPath string) (bootstrap, error) {
	props, err := readProperties(propsPath)
	if err != nil {
		return bootstrap{}, err
	}
	listeners, ok := props["listeners"]
	if !ok {
		return bootstrap{}, fmt.Errorf("server.properties %s: no listeners= line", propsPath)
	}
	name, port, err := credentialFreeListener(listeners)
	if err != nil {
		return bootstrap{}, err
	}
	return bootstrap{
		addr:     net.JoinHostPort("127.0.0.1", port),
		rewrites: advertisedRewrites(name, props["advertised.listeners"]),
	}, nil
}

// readProperties parses the Java .properties subset the Kafka scripts write:
// `key=value` or `key = value` (also `key:value`), `#`/`!` comments, last key
// wins like Kafka's own loader.
func readProperties(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("server.properties: %w", err)
	}
	defer f.Close()

	props := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		sep := strings.IndexAny(line, "=:")
		if sep < 0 {
			continue
		}
		props[strings.TrimSpace(line[:sep])] = strings.TrimSpace(line[sep+1:])
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("server.properties: %w", err)
	}
	return props, nil
}

type listener struct {
	name, hostPort, port string
}

func splitListeners(spec string) []listener {
	var out []listener
	for _, l := range strings.Split(spec, ",") {
		name, hostPort, ok := strings.Cut(strings.TrimSpace(l), "://")
		if !ok {
			continue
		}
		_, port, err := net.SplitHostPort(hostPort)
		if err != nil {
			continue
		}
		out = append(out, listener{name: strings.ToUpper(name), hostPort: hostPort, port: port})
	}
	return out
}

// credentialFreeListener picks the INTERNAL listener (SASL instances: PLAINTEXT +
// ANONYMOUS super user, never customer-reachable) or, on a legacy instance, the
// single PLAINTEXT listener. CLIENT is SASL/TLS and is never used: it would need
// customer credentials and a cert that does not name localhost.
func credentialFreeListener(listeners string) (name, port string, err error) {
	ports := map[string]string{}
	for _, l := range splitListeners(listeners) {
		ports[l.name] = l.port
	}
	for _, candidate := range []string{"INTERNAL", "PLAINTEXT"} {
		if p, ok := ports[candidate]; ok {
			return candidate, p, nil
		}
	}
	return "", "", fmt.Errorf("no INTERNAL or PLAINTEXT listener in %q (CLIENT is SASL/TLS)", listeners)
}

// bootstrapFromListeners returns the localhost dial address for `listeners=`.
func bootstrapFromListeners(listeners string) (string, error) {
	_, port, err := credentialFreeListener(listeners)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort("127.0.0.1", port), nil
}

// advertisedRewrites maps this node's advertised host:port for `name` to
// 127.0.0.1:<port>. Legacy: `PLAINTEXT://<public dns>:9092` → localhost (the
// node cannot reach its own public address through the security group). HA:
// `INTERNAL://<private ip>:9094` → localhost (harmless, one hop shorter). A
// SASL single node already advertises INTERNAL on 127.0.0.1 → identity.
func advertisedRewrites(name, advertised string) map[string]string {
	rewrites := map[string]string{}
	for _, l := range splitListeners(advertised) {
		if l.name == name {
			rewrites[l.hostPort] = net.JoinHostPort("127.0.0.1", l.port)
		}
	}
	return rewrites
}

// localRewritingDialer dials as kgo asks, except that this node's own advertised
// address is redirected to localhost. Everything else (HA peers) is untouched.
func localRewritingDialer(rewrites map[string]string) func(ctx context.Context, network, host string) (net.Conn, error) {
	d := &net.Dialer{Timeout: 3 * time.Second}
	return func(ctx context.Context, network, host string) (net.Conn, error) {
		if to, ok := rewrites[host]; ok {
			host = to
		}
		return d.DialContext(ctx, network, host)
	}
}
