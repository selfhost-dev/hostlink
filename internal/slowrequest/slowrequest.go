// Package slowrequest reports control-plane requests that take longer than a
// threshold. A request that stalls in transport and then succeeds produces no
// error, so without this the only symptom is a gap in the agent's log.
package slowrequest

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/gommon/log"

	"hostlink/internal/telemetry"
)

const (
	// MetricName is the telemetry metric emitted for a slow control-plane request.
	MetricName = "hostlink.api.request.slow"
)

// Reporter reports control-plane requests slower than a threshold.
type Reporter struct {
	threshold time.Duration
}

// New returns a Reporter that reports requests slower than threshold.
// The threshold must be resolved by the caller: a threshold of zero or less
// reports every request, which makes the mistake visible instead of hiding
// the diagnostic.
func New(threshold time.Duration) Reporter {
	return Reporter{threshold: threshold}
}

// Report logs a warning and emits a telemetry metric when the request took
// longer than the threshold. Faster requests are not reported at all, so a
// healthy agent stays quiet.
//
// The report carries the request path, the method and the elapsed
// milliseconds only: no bodies, no headers, no credentials, no query string.
func (r Reporter) Report(req *http.Request, elapsed time.Duration, err error) {
	if elapsed < r.threshold {
		return
	}

	endpoint := req.URL.Path
	method := req.Method
	durationMs := elapsed.Milliseconds()

	if err != nil {
		log.Warnf("slow control-plane request endpoint=%s method=%s duration_ms=%d error=%s",
			endpoint, method, durationMs, errorText(err, req.URL.RawQuery))
	} else {
		log.Warnf("slow control-plane request endpoint=%s method=%s duration_ms=%d",
			endpoint, method, durationMs)
	}

	telemetry.Metric(MetricName, durationMs, map[string]any{
		"endpoint": endpoint,
		"method":   method,
	})
}

// errorText renders an error for the warning line. Errors that opt in through
// LogSafe are reported without their payload; the query string is stripped
// from transport errors, which embed the full request URL.
func errorText(err error, rawQuery string) string {
	var safe interface{ LogSafe() string }
	if errors.As(err, &safe) {
		return safe.LogSafe()
	}
	text := err.Error()
	if rawQuery != "" {
		text = strings.ReplaceAll(text, "?"+rawQuery, "")
	}
	return text
}
