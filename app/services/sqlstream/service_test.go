package sqlstream

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"hostlink/internal/crypto"
	"hostlink/internal/sqlstream/e2e"
	"hostlink/internal/sqlstream/relay"
	"hostlink/internal/sqlstream/token"
	"hostlink/internal/sqlstream/wire"
)

const (
	testAgent  = "agt_8d91f075d45f41da07fa7983a8367941"
	testUser   = "user_1"
	testOrigin = "https://console.selfhost.dev"
)

// fakeAPI is the backend: a session, and the outcomes it was told.
type fakeAPI struct {
	session  *Session
	mu       sync.Mutex
	outcomes map[string][]Outcome
}

func (a *fakeAPI) Session(context.Context) (*Session, error) { return a.session, nil }
func (a *fakeAPI) Outcome(_ context.Context, run string, o Outcome) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.outcomes[run] = append(a.outcomes[run], o)
	return nil
}
func (a *fakeAPI) PlatformLogin(context.Context) (string, string, error) { return "", "", nil }

func (a *fakeAPI) final(run string) (Outcome, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, o := range a.outcomes[run] {
		if o.Status != "running" {
			return o, true
		}
	}
	return Outcome{}, false
}

// harness is a relay, the query service connected to it, and the backend's signing key.
type harness struct {
	t      *testing.T
	priv   ed25519.PrivateKey
	relay  *httptest.Server
	api    *fakeAPI
	svc    *Service
	usersD string
}

func newHarness(t *testing.T, clickhouseURL string) *harness {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	keys := token.NewKeySet(map[string]ed25519.PublicKey{"k1": pub})
	rs := relay.New(relay.Config{Keys: keys, AllowedOrigins: []string{testOrigin}})
	hs := httptest.NewServer(rs.Handler())
	t.Cleanup(func() { rs.Shutdown(); hs.Close() })

	h := &harness{t: t, priv: priv, relay: hs, usersD: t.TempDir()}
	writeFile(t, h.usersD, accountFile, "<!-- selfhost_read sync 7 -->\n<clickhouse><log_queries>1</log_queries></clickhouse>\n")
	agentTok := h.sign(token.TypeAgent, &token.Claims{Sub: testAgent})
	h.api = &fakeAPI{outcomes: map[string][]Outcome{}, session: &Session{
		Token: agentTok, RelayURL: "ws" + strings.TrimPrefix(hs.URL, "http"),
		Keys:      []token.JWK{{Kid: "k1", Kty: "OKP", Crv: "Ed25519", X: base64.RawURLEncoding.EncodeToString(pub)}},
		ExpiresAt: time.Now().Add(time.Hour),
	}}
	h.svc = New(Config{AgentID: testAgent, PrivateKey: agentKey, API: h.api, ClickHouseURL: clickhouseURL, UsersDir: h.usersD})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go h.svc.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if agents, _, _ := rs.Stats(); agents == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the service never connected to the relay")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return h
}

func (h *harness) sign(typ string, c *token.Claims) string {
	h.t.Helper()
	now := time.Now().Unix()
	c.Iss, c.Aud, c.Iat, c.Exp = token.Issuer, token.Audience, now, now+60
	if c.Jti == "" {
		c.Jti = base64.RawURLEncoding.EncodeToString([]byte(time.Now().String()))
	}
	tok, err := token.Sign(h.priv, "k1", typ, c)
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

// ticket is what the backend would make for a run of sql, sealed for the agent.
func (h *harness) ticket(runPid, sql, bpk string, lim token.Limits) string {
	sealed, err := crypto.SealEnv(map[string]string{"SQL": sql, "SQL_ROLE_PASSWORD": "pw"}, runPid, &agentKey.PublicKey)
	if err != nil {
		h.t.Fatal(err)
	}
	return h.sign(token.TypeTicket, &token.Claims{
		Sub: testUser, Agt: testAgent, Run: runPid, Qid: runPid + "_1", Ins: "awsinst_1", Eng: "clickhouse",
		DB: "analytics", Mode: "read", Kind: "select", Acct: &token.Account{User: "selfhost_read", Stamp: "7"},
		Lim: &lim, Bpk: bpk, Sealed: sealed, Jti: runPid,
	})
}

// tab is a browser tab on the relay.
type tab struct {
	t  *testing.T
	ws *websocket.Conn
}

func (h *harness) tab() *tab {
	h.t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(h.relay.URL, "http")+"/v1/browser", http.Header{"Origin": {testOrigin}})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = ws.Close() })
	tb := &tab{t: h.t, ws: ws}
	tb.send(wire.KindHello, wire.Header{}, []byte(h.sign(token.TypeBrowser, &token.Claims{Sub: testUser, Org: "org_1"})))
	if f := tb.recv(); f.Kind != wire.KindWelcome {
		h.t.Fatalf("got %s", f.Kind)
	}
	return tb
}

func (tb *tab) send(kind wire.Kind, h wire.Header, body []byte) {
	tb.t.Helper()
	msg, _ := wire.Encode(kind, h, body)
	if err := tb.ws.WriteMessage(websocket.BinaryMessage, msg); err != nil {
		tb.t.Fatal(err)
	}
}

func (tb *tab) recv() wire.Frame {
	tb.t.Helper()
	_ = tb.ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, msg, err := tb.ws.ReadMessage()
	if err != nil {
		tb.t.Fatal(err)
	}
	f, err := wire.Decode(append([]byte(nil), msg...))
	if err != nil {
		tb.t.Fatal(err)
	}
	return f
}

// result is one run as the tab saw it.
type result struct {
	rejected  string
	columns   []any
	rows      [][]any
	last      map[string]any
	lastType  string
	firstRows time.Duration
}

// run sends a ticket and reads the run to its end, acking as it goes.
func (tb *tab) run(h *harness, runPid, sql string, lim token.Limits, onFrame func(seq uint64)) result {
	tb.t.Helper()
	priv, bpk, _ := e2e.BrowserKey()
	start := time.Now()
	tb.send(wire.KindRun, wire.Header{Run: runPid}, []byte(h.ticket(runPid, sql, bpk, lim)))
	var res result
	var opener *e2e.Opener
	for {
		f := tb.recv()
		switch f.Kind {
		case wire.KindRejected, wire.KindError:
			var b struct{ Code string }
			_ = json.Unmarshal(f.Body, &b)
			res.rejected = b.Code
			return res
		case wire.KindAccepted:
			var err error
			if opener, err = e2e.Open(runPid, priv, bpk, f.Body, &agentKey.PublicKey); err != nil {
				tb.t.Fatal(err)
			}
		case wire.KindData:
			plain, err := opener.Open(f.Prefix, *f.Header.Seq, f.Body)
			if err != nil {
				tb.t.Fatal(err)
			}
			var body map[string]any
			_ = json.Unmarshal(plain, &body)
			switch f.Header.T {
			case wire.TypeColumns:
				res.columns = body["columns"].([]any)
			case wire.TypeRows:
				if res.rows == nil {
					res.firstRows = time.Since(start)
				}
				for _, r := range body["rows"].([]any) {
					res.rows = append(res.rows, r.([]any))
				}
			}
			tb.send(wire.KindAck, wire.Header{Run: runPid, Seq: f.Header.Seq}, nil)
			if onFrame != nil {
				onFrame(*f.Header.Seq)
			}
			if wire.Final(f.Header.T) {
				res.last, res.lastType = body, f.Header.T
				return res
			}
		}
	}
}

func TestServiceRunsATicketEndToEnd(t *testing.T) {
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) {
		w.data("n\tv\nUInt64\tString\n" + rowsText(0, 2500))
		w.progress(2500, 2500)
	})
	h := newHarness(t, ch.URL)
	tb := h.tab()
	res := tb.run(h, "sqr_00000000000000000001", "SELECT n, v FROM t", token.Limits{Rows: 10000, Bytes: 64 << 20, Secs: 60}, nil)
	if res.rejected != "" || len(res.rows) != 2500 || res.lastType != wire.TypeEnd || res.last["status"] != "completed" {
		t.Fatalf("rejected %q rows %d last %s %v", res.rejected, len(res.rows), res.lastType, res.last)
	}
	waitFor(t, func() bool { _, ok := h.api.final("sqr_00000000000000000001"); return ok })
	o, _ := h.api.final("sqr_00000000000000000001")
	if o.Status != "completed" || o.Rows != 2500 {
		t.Fatalf("outcome %+v", o)
	}
	if h.api.outcomes["sqr_00000000000000000001"][0].Status != "running" {
		t.Fatalf("no running outcome first: %+v", h.api.outcomes["sqr_00000000000000000001"])
	}

	// The same ticket twice is refused; a ticket for another agent is too.
	priv, bpk, _ := e2e.BrowserKey()
	_ = priv
	tk := h.ticket("sqr_00000000000000000002", "SELECT 1", bpk, token.Limits{Rows: 10, Bytes: 1 << 20, Secs: 60})
	tb.send(wire.KindRun, wire.Header{Run: "sqr_00000000000000000002"}, []byte(tk))
	for f := tb.recv(); f.Kind != wire.KindData || !wire.Final(f.Header.T); f = tb.recv() {
		if f.Kind == wire.KindData {
			tb.send(wire.KindAck, wire.Header{Run: "sqr_00000000000000000002", Seq: f.Header.Seq}, nil)
		}
	}
	tb.send(wire.KindRun, wire.Header{Run: "sqr_00000000000000000002"}, []byte(tk))
	if f := tb.recv(); f.Kind != wire.KindError {
		t.Fatalf("a reused ticket got %s", f.Kind)
	}
}

func TestServiceRefusesWhatTheScriptRefuses(t *testing.T) {
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) { w.data("n\nUInt8\n1\n") })
	h := newHarness(t, ch.URL)
	tb := h.tab()

	// Another sync's account file: role_out_of_date, before any key exchange; the backend is told.
	writeFile(t, h.usersD, accountFile, "<!-- selfhost_read sync 8 -->\n<clickhouse><log_queries>1</log_queries></clickhouse>\n")
	res := tb.run(h, "sqr_00000000000000000003", "SELECT 1", token.Limits{Rows: 10, Bytes: 1 << 20, Secs: 60}, nil)
	if res.rejected != "role_out_of_date" {
		t.Fatalf("got %q", res.rejected)
	}
	waitFor(t, func() bool { _, ok := h.api.final("sqr_00000000000000000003"); return ok })
	if o, _ := h.api.final("sqr_00000000000000000003"); o.Status != "failed" || o.Class != "role_out_of_date" {
		t.Fatalf("outcome %+v", o)
	}
	_ = os.Remove(filepath.Join(h.usersD, accountFile))
}

func TestServiceCancelAndRelayLoss(t *testing.T) {
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) {
		w.data("n\nUInt64\n")
		for i := int64(1); ; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(20 * time.Millisecond):
				w.progress(i*1000, 1_000_000)
			}
		}
	})
	h := newHarness(t, ch.URL)
	tb := h.tab()
	var once sync.Once
	var cancelAt time.Time
	res := tb.run(h, "sqr_00000000000000000004", "SELECT n FROM big", token.Limits{Rows: 10, Bytes: 1 << 20, Secs: 60}, func(seq uint64) {
		if seq >= 3 {
			once.Do(func() {
				cancelAt = time.Now()
				tb.send(wire.KindCancel, wire.Header{Run: "sqr_00000000000000000004"}, nil)
			})
		}
	})
	if res.last["status"] != "cancelled" {
		t.Fatalf("last %v", res.last)
	}
	if took := time.Since(cancelAt); took > time.Second {
		t.Fatalf("Cancel took %v", took)
	}
	waitFor(t, func() bool { _, ok := h.api.final("sqr_00000000000000000004"); return ok })
	if o, _ := h.api.final("sqr_00000000000000000004"); o.Status != "cancelled" || o.Reason != "cancelled" {
		t.Fatalf("outcome %+v", o)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func init() {
	// The service's keys parse its session's JWKs; check the fixture's key is a real SPKI too.
	if _, err := x509.MarshalPKIXPublicKey(&agentKey.PublicKey); err != nil {
		panic(err)
	}
}

func TestServiceCancelWhileQueued(t *testing.T) {
	release := make(chan struct{})
	ch := fakeClickHouse(t, func(w *packetWriter, r *http.Request) {
		w.data("n\nUInt8\n1\n")
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	defer close(release)
	h := newHarness(t, ch.URL)
	tb := h.tab()
	lim := token.Limits{Rows: 10, Bytes: 1 << 20, Secs: 60}
	// Two runs take both workers and hold them; the third waits in the queue.
	for _, run := range []string{"sqr_00000000000000000011", "sqr_00000000000000000012", "sqr_00000000000000000013"} {
		_, bpk, _ := e2e.BrowserKey()
		tb.send(wire.KindRun, wire.Header{Run: run}, []byte(h.ticket(run, "SELECT 1", bpk, lim)))
	}
	accepted := map[string]bool{}
	for len(accepted) < 2 {
		f := tb.recv()
		if f.Kind == wire.KindAccepted {
			if f.Header.Run == "sqr_00000000000000000013" {
				t.Fatal("the third run did not wait for a worker")
			}
			accepted[f.Header.Run] = true
		}
	}
	tb.send(wire.KindCancel, wire.Header{Run: "sqr_00000000000000000013"}, nil)
	// Its refusal comes once a worker is free.
	release <- struct{}{}
	release <- struct{}{}
	for {
		f := tb.recv()
		if f.Header.Run != "sqr_00000000000000000013" {
			continue
		}
		var b struct{ Code string }
		_ = json.Unmarshal(f.Body, &b)
		if f.Kind != wire.KindRejected || b.Code != "cancelled" {
			t.Fatalf("queued run got %s %q", f.Kind, b.Code)
		}
		break
	}
	waitFor(t, func() bool { _, ok := h.api.final("sqr_00000000000000000013"); return ok })
	if o, _ := h.api.final("sqr_00000000000000000013"); o.Status != "cancelled" {
		t.Fatalf("outcome %+v", o)
	}
}

func TestRelayCode(t *testing.T) {
	for body, want := range map[string]string{
		`{"code":"run_too_large"}`: "run_too_large",
		`{"code":"Bad Code"}`:      "relay_refused",
		`not json`:                 "relay_refused",
		`{}`:                       "relay_refused",
	} {
		if got := relayCode([]byte(body)); got != want {
			t.Errorf("relayCode(%s) = %s, want %s", body, got, want)
		}
	}
}
