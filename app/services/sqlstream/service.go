// Package sqlstream is hostlink's query service for the SQL stream path (sql-stream-v1): it keeps
// one outbound connection to the relay, takes the tickets the backend signed for this agent,
// runs each statement on the database's own HTTP port on loopback, and streams the rows to the
// browser tab sealed end to end. It runs beside the task runner, with workers of its own, so a
// query never waits behind a task. Contract: the selfhost backend's docs/sql-stream-protocol.md.
package sqlstream

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"hostlink/internal/crypto"
	"hostlink/internal/sqlstream/e2e"
	"hostlink/internal/sqlstream/token"
	"hostlink/internal/sqlstream/wire"
)

// Capability is what the agent tells the backend it can do, on every request
// (X-Agent-Capabilities) and when it asks for a session.
const Capability = "sql-stream-v1"

// ErrDisabled is the backend saying the path is off for this agent (404 on the session).
var ErrDisabled = errors.New("the SQL stream path is off")

// Session is what the backend hands the agent: a relay token, where the relay is, and the keys
// tickets are signed with.
type Session struct {
	Token     string      `json:"token"`
	RelayURL  string      `json:"relay_url"`
	Keys      []token.JWK `json:"keys"`
	ExpiresAt time.Time   `json:"expires_at"`
}

// API is the backend, over the agent's signed requests.
type API interface {
	Session(ctx context.Context) (*Session, error)
	Outcome(ctx context.Context, runPid string, o Outcome) error
	PlatformLogin(ctx context.Context) (user, password string, err error)
}

// Config is the service's.
type Config struct {
	AgentID       string
	PrivateKey    *rsa.PrivateKey
	API           API
	ClickHouseURL string // the server's HTTP port on loopback
	UsersDir      string // its users.d, where the account's files are
	Workers       int    // runs at once: the account's max_concurrent_queries_for_user
	Queue         int    // runs waiting for a worker
	Window        int64  // bytes sent and not yet acked before a run stops reading
	Dialer        *websocket.Dialer
	OffWait       time.Duration // between asks while the backend says the path is off
}

func (c *Config) defaults() {
	if c.ClickHouseURL == "" {
		c.ClickHouseURL = "http://127.0.0.1:8123"
	}
	if c.UsersDir == "" {
		c.UsersDir = "/etc/clickhouse-server/users.d"
	}
	if c.Workers <= 0 {
		c.Workers = 2
	}
	if c.Queue <= 0 {
		c.Queue = 8
	}
	if c.Window <= 0 {
		c.Window = 4 << 20
	}
	if c.Dialer == nil {
		c.Dialer = &websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment}
	}
	if c.OffWait <= 0 {
		c.OffWait = 15 * time.Minute
	}
}

// Service is the query service.
type Service struct {
	cfg     Config
	machine *machine
	http    *http.Client
	log     *log.Entry
	jobs    chan *run

	mu   sync.Mutex
	runs map[string]*run
	jtis map[string]time.Time
	keys *token.KeySet
	link *link
}

// New returns a service; Run starts it.
func New(cfg Config) *Service {
	cfg.defaults()
	return &Service{
		cfg:     cfg,
		machine: newMachine(cfg.UsersDir),
		http:    &http.Client{Transport: &http.Transport{Proxy: nil, MaxIdleConnsPerHost: cfg.Workers + 2}},
		log:     log.WithField("component", "sqlstream"),
		jobs:    make(chan *run, cfg.Queue),
		runs:    map[string]*run{},
		jtis:    map[string]time.Time{},
		keys:    token.NewKeySet(nil),
	}
}

// Run serves until ctx ends: asks for a session, connects to the relay, takes runs, and
// reconnects with backoff when the connection goes.
func (s *Service) Run(ctx context.Context) {
	for i := 0; i < s.cfg.Workers; i++ {
		go s.worker(ctx)
	}
	backoff := time.Second
	var sess *Session
	for ctx.Err() == nil {
		if sess == nil || time.Until(sess.ExpiresAt) < 5*time.Minute {
			fresh, err := s.cfg.API.Session(ctx)
			switch {
			case errors.Is(err, ErrDisabled):
				sess = nil
				s.log.Debug("the SQL stream path is off; asking again later")
				sleep(ctx, s.cfg.OffWait)
				continue
			case err != nil:
				s.log.WithError(err).Warn("no SQL stream session")
				sleep(ctx, jitter(backoff))
				backoff = min(backoff*2, time.Minute)
				continue
			}
			if err := s.useSession(fresh); err != nil {
				s.log.WithError(err).Warn("SQL stream session unusable")
				sleep(ctx, jitter(backoff))
				backoff = min(backoff*2, time.Minute)
				continue
			}
			sess = fresh
		}
		connected := time.Now()
		err := s.serveRelay(ctx, sess)
		if ctx.Err() != nil {
			return
		}
		if time.Since(connected) > 30*time.Second {
			backoff = time.Second
		}
		s.log.WithError(err).Info("relay connection ended; reconnecting")
		if errors.Is(err, errUnauthorized) {
			sess = nil // a fresh token next time
		}
		sleep(ctx, jitter(backoff))
		backoff = min(backoff*2, time.Minute)
	}
}

func (s *Service) useSession(sess *Session) error {
	if sess.Token == "" || !strings.HasPrefix(sess.RelayURL, "ws") {
		return errors.New("session without a token or a relay")
	}
	doc, err := json.Marshal(map[string]any{"keys": sess.Keys})
	if err != nil {
		return err
	}
	parsed, err := token.ParseJWKS(doc)
	if err != nil {
		return err
	}
	s.keys.Replace(parsed)
	return nil
}

var errUnauthorized = errors.New("the relay refused the agent's token")

// link is the relay connection: one writer, frames queued to it.
type link struct {
	ws   *websocket.Conn
	out  chan []byte
	done chan struct{}
	once sync.Once
}

func (l *link) send(msg []byte) bool {
	select {
	case l.out <- msg:
		return true
	case <-l.done:
		return false
	}
}

func (l *link) close() {
	l.once.Do(func() {
		close(l.done)
		_ = l.ws.Close()
	})
}

func (s *Service) serveRelay(ctx context.Context, sess *Session) error {
	ws, _, err := s.cfg.Dialer.DialContext(ctx, strings.TrimRight(sess.RelayURL, "/")+"/v1/agent", nil)
	if err != nil {
		return err
	}
	l := &link{ws: ws, out: make(chan []byte, 256), done: make(chan struct{})}
	defer l.close()

	hello, _ := wire.Encode(wire.KindHello, wire.Header{}, []byte(sess.Token))
	_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
	if err := ws.WriteMessage(websocket.BinaryMessage, hello); err != nil {
		return err
	}
	_ = ws.SetReadDeadline(time.Now().Add(15 * time.Second))
	_, msg, err := ws.ReadMessage()
	if err != nil {
		var ce *websocket.CloseError
		if errors.As(err, &ce) && ce.Code == 4401 {
			return errUnauthorized
		}
		return err
	}
	if f, err := wire.Decode(msg); err != nil || f.Kind != wire.KindWelcome {
		return errUnauthorized
	}
	s.log.Info("connected to the SQL relay")

	go func() {
		for {
			select {
			case msg := <-l.out:
				_ = ws.SetWriteDeadline(time.Now().Add(15 * time.Second))
				if err := ws.WriteMessage(websocket.BinaryMessage, msg); err != nil {
					l.close()
					return
				}
			case <-l.done:
				return
			}
		}
	}()

	s.mu.Lock()
	s.link = l
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.link == l {
			s.link = nil
		}
		runs := make([]*run, 0, len(s.runs))
		for _, r := range s.runs {
			if r.out == l {
				runs = append(runs, r)
			}
		}
		s.mu.Unlock()
		for _, r := range runs {
			r.stop("relay_lost")
		}
	}()

	readWindow := 75 * time.Second
	ws.SetPingHandler(func(data string) error {
		_ = ws.SetReadDeadline(time.Now().Add(readWindow))
		return ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(10*time.Second))
	})
	for {
		_ = ws.SetReadDeadline(time.Now().Add(readWindow))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		f, err := wire.Decode(msg)
		if err != nil {
			continue
		}
		s.dispatch(l, f)
	}
}

func (s *Service) dispatch(l sender, f wire.Frame) {
	if f.Kind == wire.KindRun {
		s.take(l, f)
		return
	}
	s.mu.Lock()
	r := s.runs[f.Header.Run]
	s.mu.Unlock()
	if r == nil {
		return
	}
	switch f.Kind {
	case wire.KindAck:
		if f.Header.Seq != nil {
			r.ack(*f.Header.Seq)
		}
	case wire.KindResume:
		if f.Header.Seq != nil {
			r.resume(*f.Header.Seq)
		}
	case wire.KindCancel:
		r.stop("cancelled")
	case wire.KindGone:
		r.stop("browser_gone")
	case wire.KindError:
		r.stop("relay_refused")
	}
}

func (s *Service) reject(l sender, runPid, code string) {
	body := []byte(`{"code":"` + code + `"}`)
	if msg, err := wire.Encode(wire.KindRejected, wire.Header{Run: runPid}, body); err == nil {
		l.send(msg)
	}
}

// take checks a ticket and queues its run, or refuses it at once.
func (s *Service) take(l sender, f wire.Frame) {
	runPid := f.Header.Run
	claims, err := s.keys.Verify(string(f.Body), token.TypeTicket, time.Now())
	if err != nil {
		code := "ticket_invalid"
		if token.Expired(err) {
			code = "ticket_expired"
		}
		s.log.WithFields(log.Fields{"run": runPid, "reason": err.Error()}).Info("ticket refused")
		s.reject(l, runPid, code)
		return
	}
	if err := token.CheckTicket(claims); err != nil || claims.Run != runPid {
		s.reject(l, runPid, "ticket_invalid")
		return
	}
	if claims.Agt != s.cfg.AgentID {
		s.reject(l, runPid, "not_this_agent")
		return
	}
	if claims.Eng != "clickhouse" {
		s.refuse(l, claims, "engine_unsupported")
		return
	}

	now := time.Now()
	s.mu.Lock()
	for id, until := range s.jtis {
		if now.After(until) {
			delete(s.jtis, id)
		}
	}
	if _, seen := s.jtis[claims.Jti]; seen || s.runs[runPid] != nil {
		s.mu.Unlock()
		s.reject(l, runPid, "ticket_invalid")
		return
	}
	s.jtis[claims.Jti] = now.Add(2 * time.Minute)
	r := newRun(claims, "", "", l, nil, s.cfg.Window, s.http, s.cfg.ClickHouseURL, s.kill)
	s.runs[runPid] = r
	s.mu.Unlock()

	select {
	case s.jobs <- r:
	default:
		s.forget(r)
		s.refuse(l, claims, "busy")
	}
}

// refuse rejects a run the backend signed, and tells the backend why.
func (s *Service) refuse(l sender, claims *token.Claims, code string) {
	s.reject(l, claims.Run, code)
	go s.report(claims.Run, Outcome{Status: "failed", Class: code}, 3)
}

func (s *Service) forget(r *run) {
	s.mu.Lock()
	if s.runs[r.claims.Run] == r {
		delete(s.runs, r.claims.Run)
	}
	s.mu.Unlock()
}

func (s *Service) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-s.jobs:
			s.serve(ctx, r)
		}
	}
}

func (s *Service) serve(ctx context.Context, r *run) {
	defer s.forget(r)
	l := r.out
	claims := r.claims
	logger := s.log.WithFields(log.Fields{"run": claims.Run, "qid": claims.Qid})

	if why := r.stoppedWhy(); why != "" {
		// Stopped while it waited for a worker: no key yet, so no sealed end. A tab that cancelled
		// hears it as a refusal; one that is gone hears nothing.
		if why == "cancelled" {
			s.reject(l, claims.Run, "cancelled")
		}
		go s.report(claims.Run, Outcome{Status: "cancelled", Reason: why}, 3)
		return
	}
	env, err := crypto.OpenSealedEnv(claims.Sealed, claims.Run, s.cfg.PrivateKey)
	if err != nil || env["SQL"] == "" || env["SQL_ROLE_PASSWORD"] == "" {
		logger.WithError(err).Warn("sealed part does not open")
		s.refuse(l, claims, "sealed_invalid")
		return
	}
	if code := s.machine.check(claims.Acct.User, claims.Acct.Stamp); code != "" {
		logger.WithField("code", code).Info("run refused")
		s.refuse(l, claims, code)
		return
	}
	accepted, sealer, err := e2e.Accept(claims.Run, claims.Bpk, s.cfg.PrivateKey)
	if err != nil {
		logger.WithError(err).Warn("key exchange failed")
		s.refuse(l, claims, "internal")
		return
	}
	r.sql, r.password, r.sealer = env["SQL"], env["SQL_ROLE_PASSWORD"], sealer
	r.sendAccepted(accepted)
	go s.report(claims.Run, Outcome{Status: "running"}, 1)

	o := r.execute(ctx)
	logger.WithFields(log.Fields{
		"status": o.Status, "rows": o.Rows, "truncated": o.Truncated, "reason": o.Reason,
		"class": o.Class, "code": o.Code, "ms": o.ElapsedMs,
	}).Info("run ended")
	go s.report(claims.Run, o, 3)
}

// report tells the backend how a run went, retrying.
func (s *Service) report(runPid string, o Outcome, tries int) {
	delay := time.Second
	for i := 0; i < tries; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := s.cfg.API.Outcome(ctx, runPid, o)
		cancel()
		if err == nil {
			return
		}
		if i == tries-1 {
			s.log.WithError(err).WithField("run", runPid).Warn("outcome not reported")
			return
		}
		time.Sleep(delay)
		delay *= 3
	}
}

// kill stops a cancelled run's query by id as the platform's login: the read account cannot
// read system.processes (revoked on purpose: it shows other people's statements). Dropping the
// request is what stops it; this is the second step, and it may fail.
func (s *Service) kill(queryID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	user, password, err := s.cfg.API.PlatformLogin(ctx)
	if err != nil || user == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.ClickHouseURL, "/")+"/",
		strings.NewReader("KILL QUERY WHERE query_id = '"+queryID+"' ASYNC"))
	if err != nil {
		return
	}
	req.Header.Set("X-ClickHouse-User", user)
	req.Header.Set("X-ClickHouse-Key", password)
	if resp, err := s.http.Do(req); err == nil {
		resp.Body.Close()
	}
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// jitter is d plus or minus a quarter.
func jitter(d time.Duration) time.Duration {
	return d - d/4 + time.Duration(rand.Int64N(int64(d/2)+1))
}
