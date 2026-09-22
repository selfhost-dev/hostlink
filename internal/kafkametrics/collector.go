// Package kafkametrics collects broker metrics from AutoMQ's Prometheus endpoint,
// plus consumer-group lag from the Kafka admin API.
//
// kafka_install/kafka_broker_config enable AutoMQ's Prometheus exporter bound to
// 127.0.0.1:9090 (s3.telemetry.metrics.exporter.type=prometheus). The agent runs on
// the broker box, scrapes that local endpoint, and forwards a kafka.database metric
// set in its heartbeat. Output keys match KafkaAdapter.valid_metrics on the control
// plane.
//
// NOTE: the SOURCE metric names below are AutoMQ's OpenTelemetry/Prometheus names and
// are candidate-matched (several aliases tried per field) — they MUST be verified
// against a real /metrics dump from a running broker; unmatched series simply stay
// zero, so a name miss degrades gracefully rather than breaking the scrape. The ONE
// exception is consumer-group lag: the broker has no such series, so it is computed
// through the admin API (lag.go) and OMITTED — never zeroed — when it cannot be.
package kafkametrics

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"

	"github.com/labstack/gommon/log"
)

const defaultEndpoint = "http://127.0.0.1:9090/metrics"

// Collector scrapes one broker's Prometheus endpoint.
type Collector interface {
	Collect(credential.Credential) (metrics.KafkaDatabaseMetrics, error)
}

// counterSample is one observation of a cumulative counter, kept so the next
// scrape can derive a per-second rate.
type counterSample struct {
	value float64
	at    time.Time
}

type collector struct {
	endpoint string
	client   *http.Client
	now      func() time.Time
	// lag computes consumer-group lag via the admin API (selfhost#2854); nil in
	// tests that exercise only the scrape. See lag.go.
	lag LagSource

	// The collector is long-lived (one per agent, reused across heartbeats), so
	// it holds the previous scrape of each cumulative counter to compute rates,
	// and the last consumer-lag state so failures are logged on CHANGE, not on
	// every 20 s tick (an operator debugging "why is lag missing here" needs the
	// reason on-box; the control plane only ever sees the keys absent).
	mu       sync.Mutex
	prev     map[string]counterSample
	lagState string
}

func New() Collector {
	return NewWithSources(defaultEndpoint, newAdminLagSource(defaultServerProperties))
}

// NewWithEndpoint scrapes `endpoint` with no lag source (the consumer-group keys
// are then omitted from every heartbeat).
func NewWithEndpoint(endpoint string) Collector { return NewWithSources(endpoint, nil) }

func NewWithSources(endpoint string, lag LagSource) Collector {
	return &collector{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 10 * time.Second},
		now:      time.Now,
		lag:      lag,
		prev:     map[string]counterSample{},
	}
}

func (c *collector) Collect(_ credential.Credential) (metrics.KafkaDatabaseMetrics, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint, nil)
	if err != nil {
		return metrics.KafkaDatabaseMetrics{}, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		// Broker down / exporter not up yet → Up=false, no error so the heartbeat
		// still reports liveness=false rather than dropping the whole beat.
		return metrics.KafkaDatabaseMetrics{Up: false}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return metrics.KafkaDatabaseMetrics{Up: false}, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return metrics.KafkaDatabaseMetrics{Up: false}, nil
	}

	now := c.now()
	s := parse(string(body))
	m := metrics.KafkaDatabaseMetrics{Up: true}

	// Metric names below are AutoMQ's actual OTel/Prometheus names, verified live against
	// a running broker's /metrics (2026-09-18); JMX-style names kept as fallbacks. bytes/
	// message/request series are cumulative *_total counters (AutoMQ exposes no pre-computed
	// rate), so we diff them against the previous scrape to emit a true per-second rate —
	// otherwise a *_per_sec alert rule on a monotonic total would fire once and never resolve.
	// Only genuinely-cumulative candidate names feed rate(); pre-rated JMX aliases are dropped.
	// Network throughput splits by AutoMQ's `direction` label on
	// kafka_broker_network_io_bytes_total (confirmed against the engine's own
	// Prometheus alert rules). Fall back to the unlabeled total for bytes_in on a
	// build without the label. Both are cumulative counters → per-second rate.
	netIn := s.sumWhere("kafka_broker_network_io_bytes_total", "direction", "in")
	if netIn == 0 {
		netIn = s.sum("kafka_network_io_bytes_total", "kafka_broker_network_io_bytes_total")
	}
	m.BytesInPerSec = c.rate("bytes_in", netIn, now)
	m.BytesOutPerSec = c.rate("bytes_out", s.sumWhere("kafka_broker_network_io_bytes_total", "direction", "out"), now)
	m.MessagesInPerSec = c.rate("messages_in", s.sum("kafka_message_count_total"), now)
	// Produce/fetch split by the `type` label on kafka_request_count_total.
	m.TotalProduceRequestsPerSec = c.rate("produce_requests", s.sumWhere("kafka_request_count_total", "type", "Produce"), now)
	m.TotalFetchRequestsPerSec = c.rate("fetch_requests", s.sumWhere("kafka_request_count_total", "type", "Fetch"), now)

	m.ActiveControllerCount = s.firstInt("kafka_controller_active_count", "kafka_active_controllers")
	m.OfflinePartitionsCount = s.firstInt("kafka_partition_offline_count", "kafka_partition_offline")
	m.UnderReplicatedPartitions = s.firstInt("kafka_partition_under_replicated") // usually 0 (S3-native, RF=1)
	m.GlobalPartitionCount = s.firstInt("kafka_partition_total_count", "kafka_partition_count")
	m.GlobalTopicCount = s.firstInt("kafka_topic_count", "kafka_controller_global_topic_count")
	m.LeaderCount = s.firstInt("kafka_partition_count", "kafka_leader_count")
	m.PartitionCount = s.firstInt("kafka_partition_count")

	m.RequestHandlerAvgIdlePercent = s.first("kafka_io_threads_idle_rate_1m", "kafka_request_handler_avg_idle_percent")
	m.NetworkProcessorAvgIdlePercent = s.first("kafka_network_threads_idle_rate", "kafka_network_processor_avg_idle_percent")

	m.LogSizeBytes = s.sumInt64("kafka_log_size", "kafka_partition_log_size")

	// Consumer-group keys come from the admin API, never from the scrape (the
	// broker exports no such series — selfhost#2854). Whatever could not be
	// measured is left nil and therefore omitted from the JSON: a failed query
	// omits both keys; a partial measurement (some group unmeasurable) reports
	// the group count but omits the max lag, which would otherwise under-state
	// the worst group. The control plane must see "not reported", never a 0 or
	// a lower bound that reads as "caught up".
	if c.lag != nil {
		snap, err := c.lag.GroupLags(context.Background())
		c.noteLagState(snap, err)
		if err == nil {
			groups := snap.Groups
			m.ConsumerGroupCount = &groups
			if snap.Complete {
				maxLag := snap.MaxLag
				m.MaxConsumerGroupLag = &maxLag
			}
			m.ConsumerLagSource = ConsumerLagSourceAdminAPI
		}
	}

	// AutoMQ S3 object traffic: per-stream cumulative byte counters (confirmed on a
	// live 1.7.4 scrape), summed across streams → per-second rate. This is the
	// engine's durability + cost centre, so these must be real, not zero.
	m.S3UploadSizeBytesPerSec = c.rate("s3_upload", s.sum("kafka_stream_upload_size_bytes_total"), now)
	m.S3DownloadSizeBytesPerSec = c.rate("s3_download", s.sum("kafka_stream_download_size_bytes_total"), now)

	return m, nil
}

// noteLagState logs the consumer-lag availability when it CHANGES: the first
// failure (with its reason), a change of reason, a partial measurement, and the
// recovery — never once per tick.
func (c *collector) noteLagState(snap GroupLagSnapshot, err error) {
	state := "ok"
	switch {
	case err != nil:
		state = "unavailable: " + err.Error()
	case !snap.Complete:
		state = fmt.Sprintf("partial: %d of %d consumer groups measured (skipped: %s) — max lag omitted",
			snap.Groups-len(snap.Skipped), snap.Groups, strings.Join(snap.Skipped, ", "))
	}

	c.mu.Lock()
	prev := c.lagState
	c.lagState = state
	c.mu.Unlock()
	if state == prev {
		return
	}
	if state == "ok" {
		if prev != "" {
			log.Infof("kafka consumer lag available again")
		}
		return
	}
	log.Warnf("kafka consumer lag %s", state)
}

// rate returns the per-second rate of a cumulative counter between the previous
// scrape and now, keyed by a stable field name. The first observation returns 0
// (no baseline yet). A counter reset (current < previous — e.g. a broker
// restart) returns 0 rather than a negative or spuriously huge value. A
// non-positive elapsed interval returns 0. The current sample always becomes the
// new baseline.
func (c *collector) rate(key string, current float64, now time.Time) float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	prev, ok := c.prev[key]
	c.prev[key] = counterSample{value: current, at: now}
	if !ok {
		return 0
	}
	elapsed := now.Sub(prev.at).Seconds()
	if elapsed <= 0 || current < prev.value {
		return 0
	}
	return (current - prev.value) / elapsed
}

// ── minimal Prometheus text parser (no external dep, mirrors traefikmetrics) ──

type sample struct {
	labels map[string]string
	value  float64
}

type parsed struct {
	byName map[string][]sample
}

func parse(text string) *parsed {
	p := &parsed{byName: map[string][]sample{}}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 1024*1024), 8*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, labels, value, ok := parseLine(line)
		if !ok {
			continue
		}
		p.byName[name] = append(p.byName[name], sample{labels: labels, value: value})
	}
	return p
}

func parseLine(line string) (name string, labels map[string]string, value float64, ok bool) {
	// name{labels} value   OR   name value
	labels = map[string]string{}
	metricPart := line
	if i := strings.IndexByte(line, '{'); i >= 0 {
		name = line[:i]
		rest := line[i+1:]
		j := strings.IndexByte(rest, '}')
		if j < 0 {
			return "", nil, 0, false
		}
		for _, kv := range strings.Split(rest[:j], ",") {
			eq := strings.IndexByte(kv, '=')
			if eq < 0 {
				continue
			}
			k := strings.TrimSpace(kv[:eq])
			v := strings.Trim(strings.TrimSpace(kv[eq+1:]), `"`)
			labels[k] = v
		}
		metricPart = strings.TrimSpace(rest[j+1:])
	} else {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return "", nil, 0, false
		}
		name = fields[0]
		metricPart = fields[1]
	}
	f := strings.Fields(metricPart)
	if len(f) == 0 {
		return "", nil, 0, false
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return "", nil, 0, false
	}
	return name, labels, v, true
}

// first returns the first non-zero value among the candidate names (or 0).
func (p *parsed) first(names ...string) float64 {
	for _, n := range names {
		if ss := p.byName[n]; len(ss) > 0 {
			return ss[0].value
		}
	}
	return 0
}

// sum returns the sum across all series of the first candidate name that exists.
func (p *parsed) sum(names ...string) float64 {
	for _, n := range names {
		if ss := p.byName[n]; len(ss) > 0 {
			total := 0.0
			for _, s := range ss {
				total += s.value
			}
			return total
		}
	}
	return 0
}

func (p *parsed) firstInt(names ...string) int   { return int(p.first(names...)) }
func (p *parsed) sumInt64(names ...string) int64 { return int64(p.sum(names...)) }

// sumWhere sums the series of `name` whose label[key] == val. AutoMQ splits some
// counters by a label rather than by metric name — network throughput by
// direction (in/out), requests by type (Produce/Fetch) — so a per-direction /
// per-type figure is a label-filtered sum over the one counter.
func (p *parsed) sumWhere(name, key, val string) float64 {
	total := 0.0
	for _, s := range p.byName[name] {
		if s.labels[key] == val {
			total += s.value
		}
	}
	return total
}
