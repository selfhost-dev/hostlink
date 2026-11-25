package sqlstream

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"hostlink/internal/sqlstream/e2e"
	"hostlink/internal/sqlstream/token"
	"hostlink/internal/sqlstream/wire"
)

// One run, from a ticket the agent took to its last frame (the backend's
// docs/sql-stream-protocol.md, sections 4 to 6). Its frames are sealed in order; up to the
// window of them wait for the browser's ack, and while more do, the run stops reading from the
// engine, which then waits too. Frames not yet acked are kept, so a browser that comes back
// within the relay's grace gets them again.

const (
	frameRows     = 1000
	frameBytes    = 256 << 10
	maxFrameBody  = 8 << 20 // one row may be large; no frame is larger
	progressEvery = 500 * time.Millisecond
)

var (
	errCut     = errors.New("cut at the run's limit")
	errStopped = errors.New("the run was stopped")
	errEngine  = errors.New("the engine refused")
)

// Outcome is what the backend is told of a run (spec section 8): never a row, the statement or
// a message.
type Outcome struct {
	Status      string `json:"status"`
	Rows        int64  `json:"rows"`
	Truncated   bool   `json:"truncated"`
	Reason      string `json:"reason,omitempty"`
	ElapsedMs   int64  `json:"elapsed_ms"`
	ReadRows    int64  `json:"read_rows"`
	ReadBytes   int64  `json:"read_bytes"`
	MemoryBytes int64  `json:"memory_bytes"`
	Class       string `json:"class,omitempty"`
	Code        string `json:"code,omitempty"`
}

// sender puts a frame on the agent's relay connection; false when it is gone.
type sender interface {
	send(msg []byte) bool
}

type sentFrame struct {
	seq uint64
	msg []byte
}

type run struct {
	claims   *token.Claims
	sql      string
	password string
	out      sender
	sealer   *e2e.Sealer
	window   int64
	client   *http.Client
	baseURL  string
	kill     func(queryID string)
	now      func() time.Time

	mu           sync.Mutex
	cond         *sync.Cond
	seq          uint64
	unacked      []sentFrame
	unackedBytes int64
	stopped      string // why it stopped early: cancelled, browser_gone, relay_lost, run_too_large
	cancelCtx    context.CancelFunc

	// The run's own goroutine's.
	tsv          tsvReader
	started      time.Time
	rows         int64
	textBytes    int64
	truncated    bool
	reason       string
	progress     chProgress
	lastProgress time.Time
	batch        [][]any
	batchBytes   int
	engineErr    *engineError
}

func newRun(claims *token.Claims, sql, password string, out sender, sealer *e2e.Sealer, window int64, client *http.Client, baseURL string, kill func(string)) *run {
	r := &run{
		claims: claims, sql: sql, password: password, out: out, sealer: sealer, window: window,
		client: client, baseURL: baseURL, kill: kill, now: time.Now, cancelCtx: func() {},
	}
	r.cond = sync.NewCond(&r.mu)
	return r
}

// sendAccepted sends the key exchange, frame 0.
func (r *run) sendAccepted(body []byte) {
	msg, err := wire.Encode(wire.KindAccepted, wire.Header{Run: r.claims.Run, Seq: wire.SeqOf(0)}, body)
	if err != nil {
		return
	}
	r.mu.Lock()
	r.unacked = append(r.unacked, sentFrame{seq: 0, msg: msg})
	r.unackedBytes += int64(len(msg))
	r.mu.Unlock()
	r.out.send(msg)
}

// ack drops every kept frame up to seq.
func (r *run) ack(seq uint64) {
	r.mu.Lock()
	keep := r.unacked[:0]
	var n int64
	for _, f := range r.unacked {
		if f.seq > seq {
			keep = append(keep, f)
			n += int64(len(f.msg))
		}
	}
	r.unacked, r.unackedBytes = keep, n
	r.cond.Broadcast()
	r.mu.Unlock()
}

// resume sends again every kept frame after seq, in order.
func (r *run) resume(seq uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range r.unacked {
		if f.seq > seq {
			r.out.send(f.msg)
		}
	}
}

// stop ends the run early, once: the engine's request is dropped (ClickHouse cancels a read whose
// client goes) and, for a cancel, killed by query id as a second step.
func (r *run) stop(why string) {
	r.mu.Lock()
	if r.stopped != "" {
		r.mu.Unlock()
		return
	}
	r.stopped = why
	cancel := r.cancelCtx
	r.cond.Broadcast()
	r.mu.Unlock()
	cancel()
	if why == "cancelled" && r.kill != nil {
		go r.kill(r.claims.Qid)
	}
}

func (r *run) stoppedWhy() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopped
}

// emit seals and sends a data frame, waiting while the window is full unless it is the last.
func (r *run) emit(t string, payload any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(payload); err != nil {
		return err
	}
	body := bytes.TrimRight(buf.Bytes(), "\n")

	r.mu.Lock()
	if !wire.Final(t) {
		for r.unackedBytes > r.window && r.stopped == "" {
			r.cond.Wait()
		}
		if r.stopped != "" {
			r.mu.Unlock()
			return errStopped
		}
	}
	r.seq++
	seq := r.seq
	prefix, err := wire.Prefix(wire.KindData, wire.Header{Run: r.claims.Run, Seq: wire.SeqOf(seq), T: t})
	if err != nil {
		r.seq--
		r.mu.Unlock()
		return err
	}
	msg := append(prefix, r.sealer.Seal(prefix, seq, body)...)
	r.unacked = append(r.unacked, sentFrame{seq: seq, msg: msg})
	r.unackedBytes += int64(len(msg))
	r.mu.Unlock()
	r.out.send(msg)
	return nil
}

// The engine's packets (chSink).

func (r *run) Data(text string) error {
	return r.tsv.Feed(text, r.columns, r.row)
}

func (r *run) columns(cols []Column) error {
	return r.emit(wire.TypeColumns, map[string]any{"columns": cols})
}

func (r *run) row(values []any, lineBytes int) error {
	if r.rows >= r.claims.Lim.Rows {
		r.truncated, r.reason = true, "row_cap"
		return errCut
	}
	if r.textBytes+int64(lineBytes) > r.claims.Lim.Bytes {
		r.truncated, r.reason = true, "byte_cap"
		return errCut
	}
	r.rows++
	r.textBytes += int64(lineBytes)
	r.batch = append(r.batch, values)
	r.batchBytes += lineBytes
	if len(r.batch) >= frameRows || r.batchBytes >= frameBytes {
		return r.flush()
	}
	return nil
}

// flush sends the rows waiting, split so no frame passes maxFrameBody; a single row that would is
// the byte cap.
func (r *run) flush() error {
	if len(r.batch) == 0 {
		return nil
	}
	batch := r.batch
	r.batch, r.batchBytes = nil, 0
	return r.sendRows(batch)
}

func (r *run) sendRows(batch [][]any) error {
	payload := map[string]any{"rows": batch}
	size, err := jsonSize(payload)
	if err != nil {
		return err
	}
	if size > maxFrameBody {
		if len(batch) == 1 {
			r.rows--
			r.truncated, r.reason = true, "byte_cap"
			return errCut
		}
		half := len(batch) / 2
		if err := r.sendRows(batch[:half]); err != nil {
			return err
		}
		return r.sendRows(batch[half:])
	}
	return r.emit(wire.TypeRows, payload)
}

func jsonSize(v any) (int, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	err := enc.Encode(v)
	return buf.Len(), err
}

func (r *run) Progress(p chProgress) error {
	if p.ReadRows >= r.progress.ReadRows {
		r.progress = p
	}
	if r.now().Sub(r.lastProgress) < progressEvery {
		return nil
	}
	r.lastProgress = r.now()
	return r.emit(wire.TypeProgress, r.progressPayload())
}

func (r *run) progressPayload() map[string]int64 {
	return map[string]int64{
		"read_rows": int64(r.progress.ReadRows), "read_bytes": int64(r.progress.ReadBytes),
		"total_rows": int64(r.progress.TotalRows), "elapsed_ms": r.now().Sub(r.started).Milliseconds(),
		"memory_bytes": int64(r.progress.Memory), "rows": r.rows,
	}
}

func (r *run) Exception(text string) error {
	e := parseEngineError(text, r.sql)
	r.engineErr = &e
	return errEngine
}

// execute runs the statement and sends every frame after accepted; it returns the outcome.
func (r *run) execute(ctx context.Context) Outcome {
	r.started = r.now()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(r.claims.Lim.Secs)*time.Second+15*time.Second)
	defer cancel()
	r.mu.Lock()
	r.cancelCtx = cancel
	stopped := r.stopped
	r.mu.Unlock()
	if stopped != "" {
		return r.finish(nil)
	}

	err := runClickHouse(ctx, r.client, chRequest{
		BaseURL: r.baseURL, User: r.claims.Acct.User, Password: r.password, Database: r.claims.DB,
		QueryID: r.claims.Qid, SQL: r.sql, Limit: r.claims.Lim.Rows + 1, Secs: r.claims.Lim.Secs,
	}, r)
	if err == nil {
		err = r.tsv.Finish(r.columns, r.row)
	}
	return r.finish(err)
}

func (r *run) finish(err error) Outcome {
	why := r.stoppedWhy()
	o := Outcome{
		Rows: r.rows, Truncated: r.truncated, Reason: r.reason, ElapsedMs: r.now().Sub(r.started).Milliseconds(),
		ReadRows: int64(r.progress.ReadRows), ReadBytes: int64(r.progress.ReadBytes), MemoryBytes: int64(r.progress.Memory),
	}
	cancelledByEngine := r.engineErr != nil && r.engineErr.Code == "394" // QUERY_WAS_CANCELLED

	switch {
	case why != "" && (err == nil || errors.Is(err, errStopped) || errors.Is(err, context.Canceled) || cancelledByEngine || errors.Is(err, errUnreachable)):
		// Stopped from outside: the rows already sent stay.
		o.Status, o.Reason, o.Truncated = "cancelled", why, false
		if why != "cancelled" {
			o.Class = why // browser_gone, relay_lost or the relay's refusal: the backend keeps it as the class
		}
		if why == "cancelled" {
			_ = r.flush()
			r.end(o)
		}
	case r.engineErr != nil:
		_ = r.flush()
		o.Status, o.Class, o.Code = "failed", errorClass(r.engineErr.Code), r.engineErr.Code
		_ = r.emit(wire.TypeError, map[string]any{
			"class": o.Class, "code": nullable(r.engineErr.Code), "message": r.engineErr.Message,
			"position": r.engineErr.Position, "rows": r.rows, "elapsed_ms": o.ElapsedMs,
		})
	case err == nil || errors.Is(err, errCut):
		if ferr := r.flush(); ferr != nil && !errors.Is(ferr, errCut) {
			return r.failed(o, "internal")
		}
		o.Rows, o.Truncated, o.Reason = r.rows, r.truncated, r.reason
		o.Status = "completed"
		r.end(o)
	case errors.Is(err, context.DeadlineExceeded):
		return r.failed(o, "timeout")
	case errors.Is(err, errUnreachable):
		return r.failed(o, "unreachable")
	case errors.Is(err, errWidth):
		return r.failed(o, "internal")
	default:
		return r.failed(o, "internal")
	}
	return o
}

func (r *run) failed(o Outcome, class string) Outcome {
	_ = r.flush()
	o.Status, o.Class = "failed", class
	_ = r.emit(wire.TypeError, map[string]any{
		"class": class, "code": nil, "message": failedMessage(class), "position": nil,
		"rows": r.rows, "elapsed_ms": o.ElapsedMs,
	})
	return o
}

func failedMessage(class string) string {
	switch class {
	case "timeout":
		return "The run went past its time and was stopped."
	case "unreachable":
		return "The database did not answer."
	}
	return "The run stopped on the database's machine."
}

func (r *run) end(o Outcome) {
	var reason any
	if o.Reason != "" && o.Status == "completed" {
		reason = o.Reason
	}
	_ = r.emit(wire.TypeEnd, map[string]any{
		"status": o.Status, "rows": o.Rows, "truncated": o.Truncated, "reason": reason,
		"elapsed_ms": o.ElapsedMs, "read_rows": o.ReadRows, "read_bytes": o.ReadBytes, "memory_bytes": o.MemoryBytes,
	})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
