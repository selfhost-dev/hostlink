// Package kafkametrics collects broker metrics from AutoMQ's Prometheus endpoint.
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
// zero, so a name miss degrades gracefully rather than breaking the scrape.
package kafkametrics

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"hostlink/domain/credential"
	"hostlink/domain/metrics"
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

	// The collector is long-lived (one per agent, reused across heartbeats), so
	// it holds the previous scrape of each cumulative counter to compute rates.
	mu   sync.Mutex
	prev map[string]counterSample
}

func New() Collector { return NewWithEndpoint(defaultEndpoint) }

func NewWithEndpoint(endpoint string) Collector {
	return &collector{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 10 * time.Second},
		now:      time.Now,
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
	m.BytesInPerSec = c.rate("bytes_in", s.sum("kafka_network_io_bytes_total", "kafka_broker_network_io_bytes_total"), now)
	m.BytesOutPerSec = 0 // in/out share kafka_network_io_bytes_total (direction label); split is a follow-up
	m.MessagesInPerSec = c.rate("messages_in", s.sum("kafka_message_count_total"), now)
	m.TotalProduceRequestsPerSec = c.rate("produce_requests", s.sum("kafka_request_count_total"), now)
	m.TotalFetchRequestsPerSec = 0 // produce/fetch share kafka_request_count_total (type label); split is a follow-up

	m.ActiveControllerCount = s.firstInt("kafka_controller_active_count", "kafka_active_controllers")
	m.OfflinePartitionsCount = s.firstInt("kafka_partition_offline_count", "kafka_partition_offline")
	m.UnderReplicatedPartitions = s.firstInt("kafka_partition_under_replicated") // usually 0 (S3-native, RF=1)
	m.GlobalPartitionCount = s.firstInt("kafka_partition_total_count", "kafka_partition_count")
	m.GlobalTopicCount = s.firstInt("kafka_topic_count", "kafka_controller_global_topic_count")
	m.LeaderCount = s.firstInt("kafka_partition_count", "kafka_leader_count")
	m.PartitionCount = s.firstInt("kafka_partition_count")

	m.RequestHandlerAvgIdlePercent = s.first("kafka_io_threads_idle_rate_1m", "kafka_request_handler_avg_idle_percent")
	m.NetworkProcessorAvgIdlePercent = s.first("kafka_network_threads_idle_rate", "kafka_network_processor_avg_idle_percent")

	m.ConsumerGroupCount = s.firstInt("kafka_group_count", "kafka_group_stable_count")
	m.MaxConsumerGroupLag = s.firstInt64("kafka_consumer_group_max_lag", "kafka_lag_max")
	m.LogSizeBytes = s.sumInt64("kafka_log_size", "kafka_partition_log_size")

	// AutoMQ S3 traffic — not present in the base broker /metrics dump verified so far;
	// left best-effort (stays 0 until the exact AutoMQ S3 series names are confirmed).
	m.S3UploadSizeBytesPerSec = s.first("automq_network_inbound_usage", "automq_s3_upload_size_rate")
	m.S3DownloadSizeBytesPerSec = s.first("automq_network_outbound_usage", "automq_s3_download_size_rate")

	return m, nil
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

func (p *parsed) firstInt(names ...string) int     { return int(p.first(names...)) }
func (p *parsed) firstInt64(names ...string) int64 { return int64(p.first(names...)) }
func (p *parsed) sumInt64(names ...string) int64   { return int64(p.sum(names...)) }
