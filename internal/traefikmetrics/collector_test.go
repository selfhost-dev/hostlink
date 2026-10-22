package traefikmetrics

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"testing"
)

// Traefik's default duration buckets (0.1, 0.3, 1.2, 5 s) when the proxy sets no
// --metrics.prometheus.buckets: everything faster than 100 ms is in the first one.
// hostlink#281: interpolating inside it reported p50/p95/p99 as 50/95/99 ms for a
// site answering in 0.1 ms.

func entrypointScrape(name string, under100ms, under300ms, total float64, sum float64, codes map[string]float64) string {
	var b strings.Builder
	for code, n := range codes {
		b.WriteString(`traefik_entrypoint_requests_total{code="` + code + `",entrypoint="` + name + `",method="GET",protocol="http"} ` + fmtFloat(n) + "\n")
	}
	b.WriteString(`traefik_entrypoint_request_duration_seconds_bucket{code="200",entrypoint="` + name + `",method="GET",protocol="http",le="0.1"} ` + fmtFloat(under100ms) + "\n")
	b.WriteString(`traefik_entrypoint_request_duration_seconds_bucket{code="200",entrypoint="` + name + `",method="GET",protocol="http",le="0.3"} ` + fmtFloat(under300ms) + "\n")
	b.WriteString(`traefik_entrypoint_request_duration_seconds_bucket{code="200",entrypoint="` + name + `",method="GET",protocol="http",le="1.2"} ` + fmtFloat(total) + "\n")
	b.WriteString(`traefik_entrypoint_request_duration_seconds_bucket{code="200",entrypoint="` + name + `",method="GET",protocol="http",le="5"} ` + fmtFloat(total) + "\n")
	b.WriteString(`traefik_entrypoint_request_duration_seconds_bucket{code="200",entrypoint="` + name + `",method="GET",protocol="http",le="+Inf"} ` + fmtFloat(total) + "\n")
	b.WriteString(`traefik_entrypoint_request_duration_seconds_sum{code="200",entrypoint="` + name + `",method="GET",protocol="http"} ` + fmtFloat(sum) + "\n")
	b.WriteString(`traefik_entrypoint_request_duration_seconds_count{code="200",entrypoint="` + name + `",method="GET",protocol="http"} ` + fmtFloat(total) + "\n")
	return b.String()
}

func fmtFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func newTestCollector() *traefikCollector {
	return &traefikCollector{lastReqs: make(map[string]lastRequestsEntrypoint)}
}

func near(t *testing.T, label string, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: got null, want %v", label, want)
	}
	if math.Abs(*got-want) > 0.01 {
		t.Fatalf("%s: got %v, want %v", label, *got, want)
	}
}

func TestEntrypointPercentilesInTheLowestBucketAreNotInterpolated(t *testing.T) {
	// One request answered in 0.111 ms: the QA row that read p50/p95/p99 = 50/95/99.
	scrape := entrypointScrape("websecure", 1, 1, 1, 0.000111, map[string]float64{"200": 1})
	sets, err := newTestCollector().aggregate(scrape)
	if err != nil || len(sets) != 1 {
		t.Fatalf("aggregate: %v, %d sets", err, len(sets))
	}
	m := sets[0].Metrics
	if m.P50ResponseTimeMs != nil || m.P95ResponseTimeMs != nil || m.P99ResponseTimeMs != nil {
		t.Fatalf("percentiles inside the lowest bucket must be null, got %v %v %v", m.P50ResponseTimeMs, m.P95ResponseTimeMs, m.P99ResponseTimeMs)
	}
	if m.ResponseTimeFloorMs != 100 {
		t.Fatalf("response_time_floor_ms: got %v, want 100 (the lowest bucket's edge)", m.ResponseTimeFloorMs)
	}
	if math.Abs(m.AvgResponseTimeMs-0.111) > 0.0001 {
		t.Fatalf("the average stays exact: got %v", m.AvgResponseTimeMs)
	}

	out, _ := json.Marshal(m)
	if !strings.Contains(string(out), `"p95_response_time_ms":null`) || !strings.Contains(string(out), `"response_time_floor_ms":100`) {
		t.Fatalf("the JSON says null and the floor: %s", out)
	}
}

func TestEntrypointPercentilesAboveTheLowestBucketAreInterpolatedThere(t *testing.T) {
	// 100 requests: 90 under 100 ms, 10 between 100 and 300 ms.
	scrape := entrypointScrape("websecure", 90, 100, 100, 3.5, map[string]float64{"200": 100})
	sets, _ := newTestCollector().aggregate(scrape)
	m := sets[0].Metrics
	if m.P50ResponseTimeMs != nil {
		t.Fatalf("p50 is in the lowest bucket: null, got %v", *m.P50ResponseTimeMs)
	}
	near(t, "p95", m.P95ResponseTimeMs, 200) // 100 + (95-90)/(100-90) * 200
	near(t, "p99", m.P99ResponseTimeMs, 280) // 100 + (99-90)/(100-90) * 200
	if m.ResponseTimeFloorMs != 100 {
		t.Fatalf("response_time_floor_ms: got %v", m.ResponseTimeFloorMs)
	}
}

func TestEntrypointCountsEveryClassSo3xxAddUp(t *testing.T) {
	// The QA row with requests_total 1 and no 2xx/4xx/5xx: an HTTP→HTTPS redirect.
	scrape := entrypointScrape("web", 1, 1, 1, 0.0001, map[string]float64{"301": 1})
	sets, _ := newTestCollector().aggregate(scrape)
	m := sets[0].Metrics
	if m.Requests3xx != 1 || m.Requests2xx+m.Requests3xx+m.Requests4xx+m.Requests5xx != m.RequestsTotal {
		t.Fatalf("classes must add up to the total: %+v", m)
	}
}

func TestRouterPercentilesFollowTheSameRule(t *testing.T) {
	scrape := strings.Join([]string{
		`traefik_router_requests_total{code="200",entrypoint="websecure",method="GET",protocol="http",router="app@docker",service="app@docker"} 3`,
		`traefik_router_requests_total{code="302",entrypoint="websecure",method="GET",protocol="http",router="app@docker",service="app@docker"} 1`,
		`traefik_router_request_duration_seconds_bucket{code="200",entrypoint="websecure",method="GET",protocol="http",router="app@docker",service="app@docker",le="0.1"} 4`,
		`traefik_router_request_duration_seconds_bucket{code="200",entrypoint="websecure",method="GET",protocol="http",router="app@docker",service="app@docker",le="0.3"} 4`,
		`traefik_router_request_duration_seconds_bucket{code="200",entrypoint="websecure",method="GET",protocol="http",router="app@docker",service="app@docker",le="+Inf"} 4`,
		`traefik_router_request_duration_seconds_sum{code="200",entrypoint="websecure",method="GET",protocol="http",router="app@docker",service="app@docker"} 0.02`,
		`traefik_router_request_duration_seconds_count{code="200",entrypoint="websecure",method="GET",protocol="http",router="app@docker",service="app@docker"} 4`,
	}, "\n") + "\n"
	sets, err := newTestCollector().aggregateRouters(scrape)
	if err != nil || len(sets) != 1 {
		t.Fatalf("aggregateRouters: %v, %d sets", err, len(sets))
	}
	m := sets[0].Metrics
	if m.P95ResponseTimeMs != nil || m.ResponseTimeFloorMs != 100 {
		t.Fatalf("router p95 inside the lowest bucket: null with the floor; got %v floor %v", m.P95ResponseTimeMs, m.ResponseTimeFloorMs)
	}
	if m.Requests3xx != 1 {
		t.Fatalf("router 3xx: got %d", m.Requests3xx)
	}
}
