// Package traefikmetrics scrapes Traefik's Prometheus metrics endpoint and
// reports per-entrypoint HTTP signals: request rate, error rate, and response
// time percentiles. These reflect real user traffic, not synthetic probes.
//
// Prerequisites: Traefik must have metrics enabled.
// In Coolify → Proxy settings → add: --metrics.prometheus=true
// The endpoint defaults to http://localhost:8080/metrics.
package traefikmetrics

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hostlink/config/appconf"
	domainmetrics "hostlink/domain/metrics"
)

type Collector interface {
	Collect(ctx context.Context) ([]EntrypointMetricSet, error)
}

type EntrypointMetricSet struct {
	Attributes domainmetrics.TraefikEntrypointAttributes
	Metrics    domainmetrics.TraefikEntrypointMetrics
}

type lastRequestsEntrypoint struct {
	total       int64
	collectedAt time.Time
}

type traefikCollector struct {
	endpoint string
	client   *http.Client
	mu       sync.Mutex
	lastReqs map[string]lastRequestsEntrypoint // keyed by entrypoint name
}

func New() Collector {
	return NewWithEndpoint(appconf.TraefikEndpoint())
}

func NewWithEndpoint(endpoint string) Collector {
	return &traefikCollector{
		endpoint: endpoint,
		client:   &http.Client{Timeout: 5 * time.Second},
		lastReqs: make(map[string]lastRequestsEntrypoint),
	}
}

func (tc *traefikCollector) Collect(ctx context.Context) ([]EntrypointMetricSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tc.endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := tc.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", tc.endpoint, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return tc.aggregate(string(body))
}

// ── Prometheus text format parser ────────────────────────────────────────────

type promSample struct {
	name   string
	labels map[string]string
	value  float64
}

func parseSamples(text string) []promSample {
	var out []promSample
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		s, err := parseSample(line)
		if err == nil {
			out = append(out, s)
		}
	}
	return out
}

func parseSample(line string) (promSample, error) {
	var s promSample
	braceOpen := strings.IndexByte(line, '{')

	if braceOpen == -1 {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return s, fmt.Errorf("short line")
		}
		s.name = parts[0]
		s.labels = make(map[string]string)
		v, err := strconv.ParseFloat(parts[1], 64)
		if err != nil {
			return s, err
		}
		s.value = v
		return s, nil
	}

	braceClose := strings.LastIndexByte(line, '}')
	if braceClose <= braceOpen {
		return s, fmt.Errorf("bad braces")
	}

	s.name = line[:braceOpen]
	s.labels = parseLabels(line[braceOpen+1 : braceClose])

	rest := strings.TrimSpace(line[braceClose+1:])
	parts := strings.Fields(rest)
	if len(parts) == 0 {
		return s, fmt.Errorf("no value")
	}
	v, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return s, err
	}
	s.value = v
	return s, nil
}

// parseLabels converts `key1="v1",key2="v2"` into a map.
func parseLabels(s string) map[string]string {
	labels := make(map[string]string)
	for s != "" {
		eq := strings.IndexByte(s, '=')
		if eq < 0 {
			break
		}
		key := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		if len(s) == 0 || s[0] != '"' {
			break
		}
		s = s[1:]
		end := strings.IndexByte(s, '"')
		if end < 0 {
			break
		}
		labels[key] = s[:end]
		s = s[end+1:]
		if len(s) > 0 && s[0] == ',' {
			s = s[1:]
		}
	}
	return labels
}

// ── Per-entrypoint aggregation ───────────────────────────────────────────────

type entrypointAgg struct {
	requestsTotal int64
	requests2xx   int64
	requests4xx   int64
	requests5xx   int64
	// Histogram: le (seconds) → cumulative count aggregated across all label combos
	buckets       map[float64]float64
	durationSum   float64 // total response time in seconds
	durationCount float64 // total request count from _count samples
}

func (tc *traefikCollector) aggregate(text string) ([]EntrypointMetricSet, error) {
	samples := parseSamples(text)

	eps := make(map[string]*entrypointAgg)
	ensure := func(name string) *entrypointAgg {
		if eps[name] == nil {
			eps[name] = &entrypointAgg{buckets: make(map[float64]float64)}
		}
		return eps[name]
	}

	for _, s := range samples {
		entrypoint, ok := s.labels["entrypoint"]
		if !ok {
			continue
		}
		agg := ensure(entrypoint)

		switch s.name {
		case "traefik_entrypoint_requests_total":
			count := int64(s.value)
			agg.requestsTotal += count
			switch {
			case strings.HasPrefix(s.labels["code"], "2"):
				agg.requests2xx += count
			case strings.HasPrefix(s.labels["code"], "4"):
				agg.requests4xx += count
			case strings.HasPrefix(s.labels["code"], "5"):
				agg.requests5xx += count
			}

		case "traefik_entrypoint_request_duration_seconds_bucket":
			leStr := s.labels["le"]
			if leStr == "+Inf" {
				continue // use _count instead
			}
			le, err := strconv.ParseFloat(leStr, 64)
			if err == nil {
				agg.buckets[le] += s.value
			}

		case "traefik_entrypoint_request_duration_seconds_sum":
			agg.durationSum += s.value

		case "traefik_entrypoint_request_duration_seconds_count":
			agg.durationCount += s.value
		}
	}

	now := time.Now()
	tc.mu.Lock()
	defer tc.mu.Unlock()

	var results []EntrypointMetricSet
	for epName, agg := range eps {
		m := domainmetrics.TraefikEntrypointMetrics{
			Up:            true,
			RequestsTotal: agg.requestsTotal,
			Requests2xx:   agg.requests2xx,
			Requests4xx:   agg.requests4xx,
			Requests5xx:   agg.requests5xx,
		}

		if agg.requestsTotal > 0 {
			m.ErrorRate = float64(agg.requests4xx+agg.requests5xx) / float64(agg.requestsTotal) * 100
		}

		// Average response time (ms)
		if agg.durationCount > 0 {
			m.AvgResponseTimeMs = (agg.durationSum / agg.durationCount) * 1000
		}

		// Percentiles from histogram buckets
		if agg.durationCount > 0 && len(agg.buckets) > 0 {
			m.P50ResponseTimeMs = pct(agg.buckets, agg.durationCount, 0.50) * 1000
			m.P95ResponseTimeMs = pct(agg.buckets, agg.durationCount, 0.95) * 1000
			m.P99ResponseTimeMs = pct(agg.buckets, agg.durationCount, 0.99) * 1000
		}

		// Requests/sec via delta on cumulative counter
		if prev, ok := tc.lastReqs[epName]; ok {
			elapsed := now.Sub(prev.collectedAt).Seconds()
			if elapsed > 0 && agg.requestsTotal >= prev.total {
				m.RequestsPerSecond = float64(agg.requestsTotal-prev.total) / elapsed
			}
		}
		tc.lastReqs[epName] = lastRequestsEntrypoint{total: agg.requestsTotal, collectedAt: now}

		results = append(results, EntrypointMetricSet{
			Attributes: domainmetrics.TraefikEntrypointAttributes{EntrypointName: epName},
			Metrics:    m,
		})
	}

	return results, nil
}

// pct returns the estimated p-th percentile (0–1) from a cumulative histogram.
// buckets maps upper-bound seconds → cumulative count; total is the overall count.
func pct(buckets map[float64]float64, total, p float64) float64 {
	if total <= 0 || len(buckets) == 0 {
		return 0
	}
	target := p * total

	// Collect finite bucket boundaries and sort them
	les := make([]float64, 0, len(buckets))
	for le := range buckets {
		if !math.IsInf(le, 0) {
			les = append(les, le)
		}
	}
	sort.Float64s(les)

	var prevCount, prevLe float64
	for _, le := range les {
		count := buckets[le]
		if count >= target {
			if count == prevCount {
				return prevLe
			}
			// Linear interpolation within this bucket
			fraction := (target - prevCount) / (count - prevCount)
			return prevLe + fraction*(le-prevLe)
		}
		prevCount = count
		prevLe = le
	}
	return prevLe
}


