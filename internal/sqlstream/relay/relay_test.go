package relay

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"hostlink/internal/sqlstream/token"
	"hostlink/internal/sqlstream/wire"
)

const (
	origin = "https://console.selfhost.dev"
	agent1 = "agt_8d91f075d45f41da07fa7983a8367941"
	agent2 = "agt_f2fe3011cae254c652da93412fe5179a"
	user1  = "user_1"
	user2  = "user_2"
	run1   = "sqr_0123456789abcdef0123"
	run2   = "sqr_aaaaaaaaaaaaaaaaaaaa"
)

type fixture struct {
	t    *testing.T
	srv  *Server
	http *httptest.Server
	priv ed25519.PrivateKey
	now  time.Time
}

func newFixture(t *testing.T, mod func(*Config)) *fixture {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Keys:           token.NewKeySet(map[string]ed25519.PublicKey{"k1": pub}),
		AllowedOrigins: []string{origin},
		HelloTimeout:   time.Second,
		Grace:          300 * time.Millisecond,
	}
	if mod != nil {
		mod(&cfg)
	}
	srv := New(cfg)
	hs := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		srv.Shutdown()
		hs.Close()
	})
	return &fixture{t: t, srv: srv, http: hs, priv: priv, now: time.Now()}
}

func (f *fixture) sign(typ string, c *token.Claims) string {
	f.t.Helper()
	tok, err := token.Sign(f.priv, "k1", typ, c)
	if err != nil {
		f.t.Fatal(err)
	}
	return tok
}

func (f *fixture) claims(sub string) *token.Claims {
	return &token.Claims{Iss: token.Issuer, Aud: token.Audience, Sub: sub, Iat: f.now.Unix(), Exp: f.now.Unix() + 60, Jti: sub + "-" + f.now.String()}
}

func (f *fixture) ticket(user, agent, runPid, jti string) string {
	c := f.claims(user)
	c.Jti = jti
	c.Agt, c.Run, c.Qid = agent, runPid, runPid+"_1"
	c.Ins, c.Eng, c.DB, c.Mode, c.Kind = "awsinst_1", "clickhouse", "analytics", "read", "select"
	c.Acct = &token.Account{User: "selfhost_read", Stamp: "1"}
	c.Lim = &token.Limits{Rows: 10000, Bytes: 1 << 20, Secs: 60}
	c.Bpk, c.Sealed = "BAAA", "{}"
	return f.sign(token.TypeTicket, c)
}

func (f *fixture) dial(path, originHeader string) *websocket.Conn {
	f.t.Helper()
	h := http.Header{}
	if originHeader != "" {
		h.Set("Origin", originHeader)
	}
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.http.URL, "http")+path, h)
	if err != nil {
		f.t.Fatalf("dial %s: %v", path, err)
	}
	f.t.Cleanup(func() { _ = ws.Close() })
	return ws
}

func send(t *testing.T, ws *websocket.Conn, kind wire.Kind, h wire.Header, body []byte) {
	t.Helper()
	msg, err := wire.Encode(kind, h, body)
	if err != nil {
		t.Fatal(err)
	}
	if err := ws.WriteMessage(websocket.BinaryMessage, msg); err != nil {
		t.Fatal(err)
	}
}

func recv(t *testing.T, ws *websocket.Conn) wire.Frame {
	t.Helper()
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	f, err := wire.Decode(msg)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func code(t *testing.T, f wire.Frame) string {
	t.Helper()
	if f.Kind != wire.KindError {
		t.Fatalf("got %s, want error", f.Kind)
	}
	var b struct{ Code string }
	_ = json.Unmarshal(f.Body, &b)
	return b.Code
}

func (f *fixture) agent(pid string) *websocket.Conn {
	ws := f.dial("/v1/agent", "")
	send(f.t, ws, wire.KindHello, wire.Header{}, []byte(f.sign(token.TypeAgent, f.claims(pid))))
	if w := recv(f.t, ws); w.Kind != wire.KindWelcome {
		f.t.Fatalf("agent got %s", w.Kind)
	}
	return ws
}

func (f *fixture) browser(user string) *websocket.Conn {
	ws := f.dial("/v1/browser", origin)
	send(f.t, ws, wire.KindHello, wire.Header{}, []byte(f.sign(token.TypeBrowser, f.claims(user))))
	if w := recv(f.t, ws); w.Kind != wire.KindWelcome {
		f.t.Fatalf("browser got %s", w.Kind)
	}
	return ws
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition never held")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRunRoutesBothWays(t *testing.T) {
	f := newFixture(t, nil)
	a := f.agent(agent1)
	b := f.browser(user1)

	tk := f.ticket(user1, agent1, run1, "j1")
	send(t, b, wire.KindRun, wire.Header{Run: run1}, []byte(tk))
	got := recv(t, a)
	if got.Kind != wire.KindRun || got.Header.Run != run1 || string(got.Body) != tk {
		t.Fatalf("agent got %s %+v", got.Kind, got.Header)
	}

	send(t, a, wire.KindAccepted, wire.Header{Run: run1, Seq: wire.SeqOf(0)}, []byte(`{"apk":"x","sig":"y"}`))
	send(t, a, wire.KindData, wire.Header{Run: run1, Seq: wire.SeqOf(1), T: wire.TypeRows}, []byte{1, 2, 3})
	if got := recv(t, b); got.Kind != wire.KindAccepted {
		t.Fatalf("browser got %s", got.Kind)
	}
	if got := recv(t, b); got.Kind != wire.KindData || string(got.Body) != "\x01\x02\x03" {
		t.Fatalf("browser got %s %q", got.Kind, got.Body)
	}

	send(t, b, wire.KindAck, wire.Header{Run: run1, Seq: wire.SeqOf(1)}, nil)
	send(t, b, wire.KindCancel, wire.Header{Run: run1}, nil)
	if got := recv(t, a); got.Kind != wire.KindAck || *got.Header.Seq != 1 {
		t.Fatalf("agent got %s", got.Kind)
	}
	if got := recv(t, a); got.Kind != wire.KindCancel {
		t.Fatalf("agent got %s", got.Kind)
	}

	send(t, a, wire.KindData, wire.Header{Run: run1, Seq: wire.SeqOf(2), T: wire.TypeEnd}, []byte{9})
	if got := recv(t, b); got.Header.T != wire.TypeEnd {
		t.Fatalf("browser got %+v", got.Header)
	}
	waitFor(t, func() bool { _, _, runs := f.srv.Stats(); return runs == 0 })

	// After its last frame a run is gone: a late frame goes nowhere, the next frame does.
	send(t, a, wire.KindData, wire.Header{Run: run1, Seq: wire.SeqOf(3), T: wire.TypeRows}, []byte{7})
	send(t, b, wire.KindRun, wire.Header{Run: run2}, []byte(f.ticket(user1, agent1, run2, "j2")))
	if got := recv(t, a); got.Header.Run != run2 {
		t.Fatalf("agent got %+v", got.Header)
	}
}

func TestRunRefusals(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.MaxRunsPerConn = 1 })
	f.agent(agent1)
	b := f.browser(user1)

	expired := f.claims(user1)
	expired.Iat, expired.Exp = f.now.Unix()-120, f.now.Unix()-60

	cases := []struct {
		name   string
		run    string
		ticket string
		want   string
	}{
		{"someone else's", run1, f.ticket(user2, agent1, run1, "a"), CodeNotYours},
		{"agent offline", run1, f.ticket(user1, agent2, run1, "b"), CodeAgentOffline},
		{"header names another run", run2, f.ticket(user1, agent1, run1, "c"), CodeTicketInvalid},
		{"not a ticket", run1, f.sign(token.TypeBrowser, f.claims(user1)), CodeTicketInvalid},
		{"garbage", run1, "x.y.z", CodeTicketInvalid},
	}
	for _, c := range cases {
		send(t, b, wire.KindRun, wire.Header{Run: c.run}, []byte(c.ticket))
		if got := code(t, recv(t, b)); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}

	// A ticket runs once; a second run on a connection allowed one is too many.
	send(t, b, wire.KindRun, wire.Header{Run: run1}, []byte(f.ticket(user1, agent1, run1, "once")))
	waitFor(t, func() bool { _, _, runs := f.srv.Stats(); return runs == 1 })
	send(t, b, wire.KindRun, wire.Header{Run: run2}, []byte(f.ticket(user1, agent1, run2, "once")))
	if got := code(t, recv(t, b)); got != CodeTicketInvalid {
		t.Errorf("reused ticket: %s", got)
	}
	send(t, b, wire.KindRun, wire.Header{Run: run2}, []byte(f.ticket(user1, agent1, run2, "fresh")))
	if got := code(t, recv(t, b)); got != CodeTooManyRuns {
		t.Errorf("second run: %s", got)
	}
}

func TestExpiredTicket(t *testing.T) {
	f := newFixture(t, nil)
	f.agent(agent1)
	b := f.browser(user1)
	f.now = f.now.Add(-2 * time.Minute)
	send(t, b, wire.KindRun, wire.Header{Run: run1}, []byte(f.ticket(user1, agent1, run1, "old")))
	if got := code(t, recv(t, b)); got != CodeTicketExpired {
		t.Fatalf("got %s", got)
	}
}

func TestHello(t *testing.T) {
	f := newFixture(t, nil)

	// No hello: closed after the timeout.
	ws := f.dial("/v1/agent", "")
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := ws.ReadMessage(); !websocket.IsCloseError(err, closeNoHello) {
		t.Fatalf("no hello: %v", err)
	}

	// A browser token on the agents' door is refused.
	ws = f.dial("/v1/agent", "")
	send(t, ws, wire.KindHello, wire.Header{}, []byte(f.sign(token.TypeBrowser, f.claims(user1))))
	_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, msg, err := ws.ReadMessage()
		if err != nil {
			if !websocket.IsCloseError(err, closeNoHello) {
				t.Fatalf("wrong token: %v", err)
			}
			break
		}
		if fr, _ := wire.Decode(msg); fr.Kind == wire.KindWelcome {
			t.Fatal("welcomed with a browser token")
		}
	}

	// A browser from an origin not on the list never connects; an agent never sends one.
	if _, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.http.URL, "http")+"/v1/browser", http.Header{"Origin": {"https://evil.example"}}); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin: %v", err)
	}
	if _, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(f.http.URL, "http")+"/v1/agent", http.Header{"Origin": {origin}}); err == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent with an origin: %v", err)
	}
}

func TestBrowserComesBack(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.Grace = time.Second })
	a := f.agent(agent1)
	b := f.browser(user1)
	send(t, b, wire.KindRun, wire.Header{Run: run1}, []byte(f.ticket(user1, agent1, run1, "j")))
	recv(t, a)
	_ = b.Close()
	waitFor(t, func() bool { _, browsers, _ := f.srv.Stats(); return browsers == 0 })

	// While it is away, frames go nowhere; it resumes on a new connection, as the same person.
	send(t, a, wire.KindData, wire.Header{Run: run1, Seq: wire.SeqOf(1), T: wire.TypeRows}, []byte{1})
	other := f.browser(user2)
	send(t, other, wire.KindResume, wire.Header{Run: run1, Seq: wire.SeqOf(0)}, nil)
	if got := code(t, recv(t, other)); got != CodeRunUnknown {
		t.Fatalf("someone else's resume: %s", got)
	}
	back := f.browser(user1)
	send(t, back, wire.KindResume, wire.Header{Run: run1, Seq: wire.SeqOf(0)}, nil)
	if got := recv(t, a); got.Kind != wire.KindResume || *got.Header.Seq != 0 {
		t.Fatalf("agent got %s", got.Kind)
	}
	send(t, a, wire.KindData, wire.Header{Run: run1, Seq: wire.SeqOf(1), T: wire.TypeRows}, []byte{1})
	if got := recv(t, back); got.Kind != wire.KindData || *got.Header.Seq != 1 {
		t.Fatalf("resumed browser got %s", got.Kind)
	}
}

func TestBrowserGone(t *testing.T) {
	f := newFixture(t, nil)
	a := f.agent(agent1)
	b := f.browser(user1)
	send(t, b, wire.KindRun, wire.Header{Run: run1}, []byte(f.ticket(user1, agent1, run1, "j")))
	recv(t, a)
	_ = b.Close()
	got := recv(t, a) // after the grace
	if got.Kind != wire.KindGone || got.Header.Run != run1 {
		t.Fatalf("agent got %s", got.Kind)
	}
	waitFor(t, func() bool { _, _, runs := f.srv.Stats(); return runs == 0 })
}

func TestAgentGoneAndReplaced(t *testing.T) {
	f := newFixture(t, nil)
	a := f.agent(agent1)
	b := f.browser(user1)
	send(t, b, wire.KindRun, wire.Header{Run: run1}, []byte(f.ticket(user1, agent1, run1, "j1")))
	recv(t, a)

	// A newer connection for the same agent replaces the old one, whose run ends.
	a2 := f.agent(agent1)
	if got := code(t, recv(t, b)); got != CodeAgentGone {
		t.Fatalf("got %s", got)
	}
	send(t, b, wire.KindRun, wire.Header{Run: run2}, []byte(f.ticket(user1, agent1, run2, "j2")))
	if got := recv(t, a2); got.Header.Run != run2 {
		t.Fatalf("new agent connection got %+v", got.Header)
	}
	_ = a2.Close()
	if got := code(t, recv(t, b)); got != CodeAgentGone {
		t.Fatalf("got %s", got)
	}
}

func TestRunTooLarge(t *testing.T) {
	f := newFixture(t, nil)
	a := f.agent(agent1)
	b := f.browser(user1)
	send(t, b, wire.KindRun, wire.Header{Run: run1}, []byte(f.ticket(user1, agent1, run1, "j"))) // 1 MiB of rows: 4 MiB of frames
	recv(t, a)
	chunk := make([]byte, 900<<10)
	for i := uint64(1); i <= 5; i++ {
		send(t, a, wire.KindData, wire.Header{Run: run1, Seq: wire.SeqOf(i), T: wire.TypeRows}, chunk)
	}
	for {
		got := recv(t, b)
		if got.Kind == wire.KindError {
			if c := code(t, got); c != CodeRunTooLarge {
				t.Fatalf("got %s", c)
			}
			break
		}
	}
	if got := code(t, recv(t, a)); got != CodeRunTooLarge {
		t.Fatalf("agent got %s", got)
	}
}

func TestFrameTooLargeClosesTheConnection(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.MaxBrowserFrame = 1024 })
	b := f.browser(user1)
	send(t, b, wire.KindRun, wire.Header{Run: run1}, make([]byte, 4096))
	_ = b.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := b.ReadMessage(); err == nil {
		t.Fatal("a frame over the limit was taken")
	}
}

func TestUnexpectedKindClosesTheConnection(t *testing.T) {
	f := newFixture(t, nil)
	a := f.agent(agent1)
	send(t, a, wire.KindRun, wire.Header{Run: run1}, []byte("x")) // agents do not start runs
	_ = a.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := a.ReadMessage(); !websocket.IsCloseError(err, closeProtocol) {
		t.Fatalf("got %v", err)
	}
}

// After a network drop the old socket can look open to the relay until its read deadline; a
// resume from a newer connection of the same person takes the run over at once.
func TestResumeFromANewConnectionWhileTheOldOneLooksOpen(t *testing.T) {
	f := newFixture(t, nil)
	a := f.agent(agent1)
	old := f.browser(user1)
	send(t, old, wire.KindRun, wire.Header{Run: run1}, []byte(f.ticket(user1, agent1, run1, "j")))
	recv(t, a)

	fresh := f.browser(user1) // the old one never closed
	send(t, fresh, wire.KindResume, wire.Header{Run: run1, Seq: wire.SeqOf(0)}, nil)
	if got := recv(t, a); got.Kind != wire.KindResume {
		t.Fatalf("agent got %s", got.Kind)
	}
	send(t, a, wire.KindData, wire.Header{Run: run1, Seq: wire.SeqOf(1), T: wire.TypeRows}, []byte{1})
	if got := recv(t, fresh); got.Kind != wire.KindData {
		t.Fatalf("the new connection got %s", got.Kind)
	}
	// The old connection's cancel no longer reaches the run; the new one's does.
	send(t, old, wire.KindCancel, wire.Header{Run: run1}, nil)
	send(t, fresh, wire.KindCancel, wire.Header{Run: run1}, nil)
	if got := recv(t, a); got.Kind != wire.KindCancel {
		t.Fatalf("agent got %s", got.Kind)
	}
	// A resume on the connection that already holds the run is not one.
	send(t, fresh, wire.KindResume, wire.Header{Run: run1, Seq: wire.SeqOf(0)}, nil)
	if got := code(t, recv(t, fresh)); got != CodeRunUnknown {
		t.Fatalf("got %s", got)
	}
}
