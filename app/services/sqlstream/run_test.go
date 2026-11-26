package sqlstream

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"hostlink/internal/sqlstream/e2e"
	"hostlink/internal/sqlstream/token"
	"hostlink/internal/sqlstream/wire"
)

const testRun = "sqr_0123456789abcdef0123"

var agentKey = func() *rsa.PrivateKey {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return k
}()

// collector is a sender that keeps every frame.
type collector struct {
	mu     sync.Mutex
	frames [][]byte
	added  chan struct{}
}

func newCollector() *collector { return &collector{added: make(chan struct{}, 1<<16)} }

func (c *collector) send(msg []byte) bool {
	c.mu.Lock()
	c.frames = append(c.frames, append([]byte(nil), msg...))
	c.mu.Unlock()
	c.added <- struct{}{}
	return true
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.frames)
}

func (c *collector) all() [][]byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([][]byte(nil), c.frames...)
}

// opened is a frame as the browser reads it.
type opened struct {
	kind wire.Kind
	seq  uint64
	t    string
	body map[string]any
}

type browserSide struct {
	t      *testing.T
	priv   *ecdh.PrivateKey
	bpk    string
	opener *e2e.Opener
}

func newClaims(bpk string, lim token.Limits) *token.Claims {
	return &token.Claims{
		Iss: token.Issuer, Aud: token.Audience, Sub: "user_1", Iat: time.Now().Unix(), Exp: time.Now().Unix() + 60, Jti: "j",
		Agt: "agt_8d91f075d45f41da07fa7983a8367941", Run: testRun, Qid: testRun + "_1", Ins: "awsinst_1",
		Eng: "clickhouse", DB: "analytics", Mode: "read", Kind: "select",
		Acct: &token.Account{User: "selfhost_read", Stamp: "1"}, Lim: &lim, Bpk: bpk, Sealed: "{}",
	}
}

// newTestRun makes a run against chURL, sends accepted, and returns the browser that opens it.
func newTestRun(t *testing.T, chURL string, lim token.Limits, window int64, kill func(string)) (*run, *collector, *browserSide) {
	t.Helper()
	priv, bpk, err := e2e.BrowserKey()
	if err != nil {
		t.Fatal(err)
	}
	claims := newClaims(bpk, lim)
	accepted, sealer, err := e2e.Accept(testRun, bpk, agentKey)
	if err != nil {
		t.Fatal(err)
	}
	out := newCollector()
	r := newRun(claims, "SELECT 1", "pw", out, sealer, window, &http.Client{}, chURL, kill)
	r.sendAccepted(accepted)
	opener, err := e2e.Open(testRun, priv, bpk, accepted, &agentKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return r, out, &browserSide{t: t, priv: priv, bpk: bpk, opener: opener}
}

func (b *browserSide) open(msgs [][]byte) []opened {
	b.t.Helper()
	var out []opened
	var next uint64
	for _, msg := range msgs {
		f, err := wire.Decode(msg)
		if err != nil {
			b.t.Fatal(err)
		}
		if f.Header.Seq == nil || *f.Header.Seq != next {
			b.t.Fatalf("frame %s seq %v, want %d", f.Kind, f.Header.Seq, next)
		}
		next++
		o := opened{kind: f.Kind, seq: *f.Header.Seq, t: f.Header.T}
		if f.Kind == wire.KindData {
			plain, err := b.opener.Open(f.Prefix, o.seq, f.Body)
			if err != nil {
				b.t.Fatalf("frame %d does not open: %v", o.seq, err)
			}
			if err := json.Unmarshal(plain, &o.body); err != nil {
				b.t.Fatal(err)
			}
		}
		out = append(out, o)
	}
	return out
}

// fakeClickHouse answers with packets a script writes, flushed one by one.
func fakeClickHouse(t *testing.T, script func(w *packetWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		script(&packetWriter{w: w, f: w.(http.Flusher)}, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

type packetWriter struct {
	w http.ResponseWriter
	f http.Flusher
}

func (p *packetWriter) packet(v map[string]any) {
	b, _ := json.Marshal(v)
	_, _ = p.w.Write(append(b, '\n'))
	p.f.Flush()
}

func (p *packetWriter) data(text string) { p.packet(map[string]any{"packet": "data", "data": text}) }

func (p *packetWriter) progress(readRows, total int64) {
	p.packet(map[string]any{"packet": "progress", "progress": map[string]string{
		"read_rows": fmt.Sprint(readRows), "read_bytes": fmt.Sprint(readRows * 8), "total_rows_to_read": fmt.Sprint(total),
		"elapsed_ns": "1000000", "memory_usage": "4096",
	}})
}

func rowsText(from, to int) string {
	var b strings.Builder
	for i := from; i < to; i++ {
		fmt.Fprintf(&b, "%d\tv%d\n", i, i)
	}
	return b.String()
}

func execute(r *run) Outcome {
	return r.execute(context.Background())
}

func TestRunStreams(t *testing.T) {
	var seen *http.Request
	var body string
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) {
		seen = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		w.progress(100, 2500)
		w.data("n\tv\nUInt64\tString\n" + rowsText(0, 1200))
		w.progress(1200, 2500)
		w.data(rowsText(1200, 2500))
		w.progress(2500, 2500)
	})
	r, out, browser := newTestRun(t, ch.URL, token.Limits{Rows: 10000, Bytes: 64 << 20, Secs: 60}, 64<<20, nil)
	o := execute(r)
	if o.Status != "completed" || o.Rows != 2500 || o.Truncated {
		t.Fatalf("outcome %+v", o)
	}
	q := seen.URL.Query()
	if q.Get("limit") != "10001" || q.Get("max_execution_time") != "60" || q.Get("query_id") != testRun+"_1" ||
		seen.Header.Get("X-ClickHouse-User") != "selfhost_read" || seen.Header.Get("X-ClickHouse-Key") != "pw" || body != "SELECT 1" {
		t.Fatalf("request %v %v %q", q, seen.Header, body)
	}

	frames := browser.open(out.all())
	// accepted first; progress may come before the columns (a long query reports before any row),
	// and the columns before any row.
	if frames[0].kind != wire.KindAccepted {
		t.Fatalf("first frame %+v", frames[0])
	}
	for _, f := range frames[1:] {
		if f.t == wire.TypeRows {
			t.Fatal("rows before the columns")
		}
		if f.t == wire.TypeColumns {
			break
		}
	}
	var rows int
	var sawProgress bool
	for _, f := range frames[1:] {
		switch f.t {
		case wire.TypeRows:
			n := len(f.body["rows"].([]any))
			if n > frameRows {
				t.Fatalf("a frame of %d rows", n)
			}
			rows += n
		case wire.TypeProgress:
			sawProgress = true
			if f.body["total_rows"].(float64) != 2500 {
				t.Fatalf("progress %v", f.body)
			}
		}
	}
	last := frames[len(frames)-1]
	if rows != 2500 || !sawProgress || last.t != wire.TypeEnd || last.body["status"] != "completed" || last.body["rows"].(float64) != 2500 {
		t.Fatalf("rows %d progress %v last %+v", rows, sawProgress, last)
	}
	first := frames[2]
	for _, f := range frames {
		if f.t == wire.TypeRows {
			first = f
			break
		}
	}
	if got := first.body["rows"].([]any)[0].([]any); got[0] != "0" || got[1] != "v0" {
		t.Fatalf("first row %v", got)
	}
}

func TestRunCaps(t *testing.T) {
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) {
		w.data("n\tv\nUInt64\tString\n" + rowsText(0, 11))
	})
	r, out, browser := newTestRun(t, ch.URL, token.Limits{Rows: 10, Bytes: 64 << 20, Secs: 60}, 64<<20, nil)
	if o := execute(r); o.Status != "completed" || o.Rows != 10 || !o.Truncated || o.Reason != "row_cap" {
		t.Fatalf("row cap %+v", o)
	}
	end := browser.open(out.all())
	if last := end[len(end)-1]; last.body["truncated"] != true || last.body["reason"] != "row_cap" {
		t.Fatalf("end %+v", last.body)
	}

	// Bytes: each line here is 5 bytes ("0\tv0\n"); 20 bytes take four rows exactly, and the fifth
	// would pass them.
	r, _, _ = newTestRun(t, ch.URL, token.Limits{Rows: 10000, Bytes: 20, Secs: 60}, 64<<20, nil)
	if o := execute(r); o.Rows != 4 || !o.Truncated || o.Reason != "byte_cap" {
		t.Fatalf("byte cap %+v", o)
	}
}

func TestRunEngineError(t *testing.T) {
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) {
		w.data("n\tv\nUInt64\tString\n" + rowsText(0, 5))
		w.packet(map[string]any{"packet": "exception", "exception": "Code: 241. DB::Exception: Memory limit (for query) exceeded: would use 1.00 GiB. (MEMORY_LIMIT_EXCEEDED) (version 26.8.45.6 (official build))"})
	})
	r, out, browser := newTestRun(t, ch.URL, token.Limits{Rows: 10000, Bytes: 64 << 20, Secs: 60}, 64<<20, nil)
	o := execute(r)
	if o.Status != "failed" || o.Class != "memory" || o.Code != "241" || o.Rows != 5 {
		t.Fatalf("outcome %+v", o)
	}
	frames := browser.open(out.all())
	last := frames[len(frames)-1]
	if last.t != wire.TypeError || last.body["class"] != "memory" || last.body["code"] != "241" ||
		strings.Contains(last.body["message"].(string), "MEMORY_LIMIT_EXCEEDED") || last.body["rows"].(float64) != 5 {
		t.Fatalf("error frame %+v", last.body)
	}
	// The rows before it were sent first.
	if frames[len(frames)-2].t != wire.TypeRows {
		t.Fatalf("before the error: %s", frames[len(frames)-2].t)
	}

	// Refused before it ran: ClickHouse answers with an HTTP error and its text.
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("Code: 516. DB::Exception: selfhost_read: Authentication failed: password is incorrect. (AUTHENTICATION_FAILED) (version 26.8.45.6 (official build))\n"))
	}))
	defer login.Close()
	r, _, _ = newTestRun(t, login.URL, token.Limits{Rows: 10, Bytes: 1 << 20, Secs: 60}, 64<<20, nil)
	if o := execute(r); o.Class != "login_failed" || o.Status != "failed" {
		t.Fatalf("login %+v", o)
	}

	// Refused before its first packet with framing on (26.8.47, seen on QA): an HTTP error whose
	// body is one exception packet. The tab gets ClickHouse's number and words, not the packet
	// around them (read as plain text, it was class internal: "The run didn't finish").
	framed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson; payload=string")
		w.Header().Set("X-ClickHouse-Exception-Code", "47")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"packet":"exception","exception":"Code: 47. DB::Exception: Unknown expression or function identifier ` +
			"`eventt`" + ` in scope SELECT 1. Maybe you meant: ['event']. (UNKNOWN_IDENTIFIER) (version 26.8.47.5 (official build))"}` + "\n"))
	}))
	defer framed.Close()
	r, out, browser = newTestRun(t, framed.URL, token.Limits{Rows: 10, Bytes: 1 << 20, Secs: 60}, 64<<20, nil)
	if o := execute(r); o.Status != "failed" || o.Class != "engine" || o.Code != "47" {
		t.Fatalf("framed refusal %+v", o)
	}
	frames = browser.open(out.all())
	last = frames[len(frames)-1]
	msg, _ := last.body["message"].(string)
	if last.t != wire.TypeError || last.body["class"] != "engine" || last.body["code"] != "47" ||
		!strings.HasPrefix(msg, "Unknown expression or function identifier `eventt`") ||
		strings.Contains(msg, "packet") || strings.Contains(msg, "UNKNOWN_IDENTIFIER") {
		t.Fatalf("framed refusal frame %+v", last.body)
	}
}

func TestRunBackPressure(t *testing.T) {
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) {
		w.data("n\tv\nUInt64\tString\n")
		for i := 0; i < 40; i++ {
			w.data(rowsText(i*1000, (i+1)*1000))
		}
	})
	r, out, _ := newTestRun(t, ch.URL, token.Limits{Rows: 100000, Bytes: 64 << 20, Secs: 60}, 64<<10, nil)
	done := make(chan Outcome, 1)
	go func() { done <- execute(r) }()

	// No acks: the run stops sending once 64 KiB wait, long before 40,000 rows.
	time.Sleep(300 * time.Millisecond)
	stalled := out.count()
	select {
	case o := <-done:
		t.Fatalf("ran to the end without acks: %+v", o)
	default:
	}
	if stalled > 10 {
		t.Fatalf("%d frames sent without an ack", stalled)
	}
	// Acks let it finish.
	for {
		select {
		case o := <-done:
			if o.Status != "completed" || o.Rows != 40000 {
				t.Fatalf("outcome %+v", o)
			}
			return
		case <-out.added:
			r.ack(uint64(out.count() - 1))
		case <-time.After(5 * time.Second):
			t.Fatal("never finished")
		}
	}
}

func TestRunCancel(t *testing.T) {
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) {
		w.data("n\tv\nUInt64\tString\n")
		for i := int64(1); ; i++ {
			select {
			case <-r.Context().Done():
				return // the agent dropped the request: ClickHouse cancels the read
			case <-time.After(20 * time.Millisecond):
				w.progress(i*1000, 1_000_000)
			}
		}
	})
	killed := make(chan string, 1)
	r, out, browser := newTestRun(t, ch.URL, token.Limits{Rows: 10000, Bytes: 1 << 20, Secs: 60}, 64<<20, func(qid string) { killed <- qid })
	done := make(chan Outcome, 1)
	go func() { done <- execute(r) }()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	r.stop("cancelled")
	var o Outcome
	select {
	case o = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not stop the run")
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("cancel took %v", took)
	}
	if o.Status != "cancelled" || o.Reason != "cancelled" || o.Class != "" {
		t.Fatalf("outcome %+v", o)
	}
	if qid := <-killed; qid != testRun+"_1" {
		t.Fatalf("killed %s", qid)
	}
	frames := browser.open(out.all())
	if last := frames[len(frames)-1]; last.t != wire.TypeEnd || last.body["status"] != "cancelled" {
		t.Fatalf("last %+v", last)
	}

	// A tab that is gone: no frame after, the outcome says so.
	r, out, _ = newTestRun(t, ch.URL, token.Limits{Rows: 10000, Bytes: 1 << 20, Secs: 60}, 64<<20, nil)
	go func() { done <- execute(r) }()
	time.Sleep(60 * time.Millisecond)
	before := out.count()
	r.stop("browser_gone")
	if o := <-done; o.Status != "cancelled" || o.Reason != "browser_gone" || o.Class != "browser_gone" {
		t.Fatalf("gone %+v", o)
	}
	if after := out.count(); after > before+1 {
		t.Fatalf("%d frames after the tab went", after-before)
	}
}

func TestRunResume(t *testing.T) {
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) {
		w.data("n\tv\nUInt64\tString\n" + rowsText(0, 3000))
	})
	r, out, browser := newTestRun(t, ch.URL, token.Limits{Rows: 10000, Bytes: 64 << 20, Secs: 60}, 64<<20, nil)
	execute(r)
	sent := out.all()
	r.ack(1) // accepted and columns are in
	r.resume(1)
	again := out.all()[len(sent):]
	if len(again) != len(sent)-2 {
		t.Fatalf("resent %d of %d", len(again), len(sent)-2)
	}
	// The browser that comes back opens them from seq 2 on, in order.
	all := append(append([][]byte{}, sent[:2]...), again...)
	frames := browser.open(all)
	if frames[len(frames)-1].t != wire.TypeEnd {
		t.Fatalf("last %+v", frames[len(frames)-1])
	}
}

func TestRunUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close() // nothing listens there now
	r, out, browser := newTestRun(t, url, token.Limits{Rows: 10, Bytes: 1 << 20, Secs: 60}, 64<<20, nil)
	if o := execute(r); o.Status != "failed" || o.Class != "unreachable" {
		t.Fatalf("outcome %+v", o)
	}
	frames := browser.open(out.all())
	if last := frames[len(frames)-1]; last.t != wire.TypeError || last.body["class"] != "unreachable" {
		t.Fatalf("last %+v", last.body)
	}
}
