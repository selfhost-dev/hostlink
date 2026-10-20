// Package slowrequest reports control-plane requests that take longer than a
// threshold. A request that stalls in transport and then succeeds produces no
// error, so without this the only symptom is a gap in the agent's log.
//
// A slow request is reported three ways:
//   - counted in Counts, which the heartbeat drains and sends to the control
//     plane, so the platform sees slow requests from every host;
//   - a telemetry metric (written only where HOSTLINK_WS_TELEMETRY_PATH is set,
//     which today means test harnesses);
//   - one line in the agent's journal: a WARN for a slow success, or, for a
//     slow failure, the duration added to the returned error so the caller's
//     own error line says how long it took.
package slowrequest

import (
	"fmt"
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
	counts    *Counts
}

// New returns a Reporter that reports requests slower than threshold into the
// process-wide Default counts.
// The threshold must be resolved by the caller: a threshold of zero or less
// reports every request, which makes the mistake visible instead of hiding
// the diagnostic.
func New(threshold time.Duration) Reporter {
	return NewWithCounts(threshold, Default)
}

// NewWithCounts returns a Reporter that counts into counts.
func NewWithCounts(threshold time.Duration, counts *Counts) Reporter {
	return Reporter{threshold: threshold, counts: counts}
}

// Report reports a request that took longer than the threshold and returns
// the error the caller should return. Faster requests are not reported at
// all and their error is returned unchanged, so a healthy agent stays quiet.
//
// A slow failure is not logged here: every caller logs the error it returns,
// so the duration is added to that error instead of writing a second line.
//
// The report carries the request path, the method and the elapsed
// milliseconds only: no bodies, no headers, no credentials, no query string.
func (r Reporter) Report(req *http.Request, elapsed time.Duration, err error) error {
	if elapsed < r.threshold {
		return err
	}

	method := req.Method
	durationMs := elapsed.Milliseconds()
	endpoint := Endpoint(req.URL.Path)

	r.counts.record(endpoint, method, durationMs)
	telemetry.Metric(MetricName, durationMs, map[string]any{
		"endpoint": endpoint,
		"method":   method,
	})

	if err != nil {
		return &Error{Err: err, Elapsed: elapsed}
	}

	log.Warnf("slow control-plane request endpoint=%s method=%s duration_ms=%d",
		req.URL.Path, method, durationMs)
	return nil
}

// Error is a control-plane request failure that also took longer than the
// slow-request threshold.
type Error struct {
	Err     error
	Elapsed time.Duration
}

func (e *Error) Error() string {
	return fmt.Sprintf("%v (after %dms)", e.Err, e.Elapsed.Milliseconds())
}

func (e *Error) Unwrap() error {
	return e.Err
}

// Path segments that are followed by an identifier, and the literal words
// that can follow them instead.
var (
	idFollows = map[string]bool{"agents": true, "instances": true, "tasks": true}
	notAnID   = map[string]bool{"instances": true, "register": true}
)

// Endpoint groups the identifiers out of a control-plane path, so that every
// agent reports /api/v1/agents/:id/heartbeat rather than its own agent ID.
func Endpoint(path string) string {
	segments := strings.Split(path, "/")
	for i := 1; i < len(segments); i++ {
		if segments[i] == "" || !idFollows[segments[i-1]] || notAnID[segments[i]] {
			continue
		}
		segments[i] = ":id"
	}
	return strings.Join(segments, "/")
}
