// Package relay passes the SQL stream path's frames between browser tabs and agents. Agents
// dial out to it, so nothing reaches into a customer's network; a tab connects to it; a ticket
// the backend signed names the agent a run goes to. It reads frame headers and the ticket's
// signed claims, never a sealed body, and stores nothing: a run lives here only while it runs.
// Contract: the selfhost backend's docs/sql-stream-protocol.md, section 2.
package relay

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"

	"hostlink/internal/sqlstream/token"
	"hostlink/internal/sqlstream/wire"
)

// Refusal codes the relay sends (spec section 7).
const (
	CodeUnauthorized  = "unauthorized"
	CodeTicketInvalid = "ticket_invalid"
	CodeTicketExpired = "ticket_expired"
	CodeNotYours      = "not_yours"
	CodeAgentOffline  = "agent_offline"
	CodeAgentGone     = "agent_gone"
	CodeTooManyRuns   = "too_many_runs"
	CodeRunTooLarge   = "run_too_large"
	CodeRunUnknown    = "run_unknown"
	CodeProtocol      = "protocol"
)

// WebSocket close codes the relay uses.
const (
	closeNoHello   = 4401
	closeProtocol  = 4400
	closeReplaced  = 4409
	closeSlow      = 4408
	closeRestarted = websocket.CloseServiceRestart
)

// Config is the relay's. Zero values take the defaults New fills in.
type Config struct {
	Keys            *token.KeySet
	AllowedOrigins  []string // a browser's Origin must be one of these, exactly
	HelloTimeout    time.Duration
	Grace           time.Duration // how long a run waits for its tab to come back
	MaxRunsPerConn  int
	MaxAgentFrame   int64
	MaxBrowserFrame int64
	JTIWindow       time.Duration
	PingEvery       time.Duration
	WriteTimeout    time.Duration
	QueueBytes      int64 // bytes a connection may have waiting to be written
	Now             func() time.Time
}

func (c *Config) defaults() {
	if c.HelloTimeout == 0 {
		c.HelloTimeout = 10 * time.Second
	}
	if c.Grace == 0 {
		c.Grace = 20 * time.Second
	}
	if c.MaxRunsPerConn == 0 {
		c.MaxRunsPerConn = 8
	}
	if c.MaxAgentFrame == 0 {
		c.MaxAgentFrame = 8<<20 + 64<<10 // a wide row's frame, its header and its tag
	}
	if c.MaxBrowserFrame == 0 {
		c.MaxBrowserFrame = 256<<10 + wire.MaxHeader + 4
	}
	if c.JTIWindow == 0 {
		c.JTIWindow = 2 * time.Minute
	}
	if c.PingEvery == 0 {
		c.PingEvery = 25 * time.Second
	}
	if c.WriteTimeout == 0 {
		c.WriteTimeout = 10 * time.Second
	}
	if c.QueueBytes == 0 {
		c.QueueBytes = 48 << 20
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

type side int

const (
	sideAgent side = iota
	sideBrowser
)

func (s side) String() string {
	if s == sideAgent {
		return "agent"
	}
	return "browser"
}

type conn struct {
	id     string
	side   side
	ws     *websocket.Conn
	sub    string // the agent pid, or the person's user pid; set by hello
	send   chan []byte
	queued atomic.Int64
	done   chan struct{}
	once   sync.Once
	runs   map[string]*run // guarded by Server.mu
	closed bool            // guarded by Server.mu
}

type run struct {
	pid      string
	user     string
	agentPid string
	agent    *conn
	browser  *conn // nil while waiting for its tab to come back
	limit    int64
	bytes    int64
	frames   int64
	started  time.Time
	grace    *time.Timer
}

// Server is a relay.
type Server struct {
	cfg      Config
	upgrader websocket.Upgrader
	log      *log.Entry

	mu       sync.Mutex
	agents   map[string]*conn
	browsers map[*conn]struct{}
	runs     map[string]*run
	jtis     map[string]time.Time
	nextID   atomic.Uint64
	stopping bool
}

// New returns a relay.
func New(cfg Config) *Server {
	cfg.defaults()
	s := &Server{
		cfg:      cfg,
		log:      log.WithField("component", "sqlrelay"),
		agents:   map[string]*conn{},
		browsers: map[*conn]struct{}{},
		runs:     map[string]*run{},
		jtis:     map[string]time.Time{},
	}
	allowed := map[string]bool{}
	for _, o := range cfg.AllowedOrigins {
		allowed[o] = true
	}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  32 << 10,
		WriteBufferSize: 32 << 10,
		CheckOrigin: func(r *http.Request) bool {
			origin := r.Header.Get("Origin")
			if r.URL.Path == "/v1/agent" {
				return origin == "" // an agent is not a browser
			}
			return allowed[origin]
		},
	}
	return s
}

// Handler serves /v1/agent, /v1/browser and /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/agent", func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, sideAgent) })
	mux.HandleFunc("/v1/browser", func(w http.ResponseWriter, r *http.Request) { s.serve(w, r, sideBrowser) })
	mux.HandleFunc("/healthz", s.health)
	return mux
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	body, _ := json.Marshal(map[string]int{"agents": len(s.agents), "browsers": len(s.browsers), "runs": len(s.runs)})
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

// Shutdown closes every connection with "service restart", so agents and tabs reconnect.
func (s *Server) Shutdown() {
	s.mu.Lock()
	s.stopping = true
	conns := make([]*conn, 0, len(s.agents)+len(s.browsers))
	for _, c := range s.agents {
		conns = append(conns, c)
	}
	for c := range s.browsers {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		s.close(c, closeRestarted, "restarting")
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request, sd side) {
	s.mu.Lock()
	stopping := s.stopping
	s.mu.Unlock()
	if stopping {
		http.Error(w, "restarting", http.StatusServiceUnavailable)
		return
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // the upgrader answered
	}
	c := &conn{
		id:   strconv.FormatUint(s.nextID.Add(1), 36),
		side: sd,
		ws:   ws,
		send: make(chan []byte, 1024),
		done: make(chan struct{}),
		runs: map[string]*run{},
	}
	limit := s.cfg.MaxBrowserFrame
	if sd == sideAgent {
		limit = s.cfg.MaxAgentFrame
	}
	ws.SetReadLimit(limit)
	go s.writeLoop(c)
	s.readLoop(c)
}

func (s *Server) writeLoop(c *conn) {
	ping := time.NewTicker(s.cfg.PingEvery)
	defer ping.Stop()
	for {
		select {
		case msg := <-c.send:
			_ = c.ws.SetWriteDeadline(s.cfg.Now().Add(s.cfg.WriteTimeout))
			err := c.ws.WriteMessage(websocket.BinaryMessage, msg)
			c.queued.Add(-int64(len(msg)))
			if err != nil {
				s.close(c, websocket.CloseAbnormalClosure, "write failed")
				return
			}
		case <-ping.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, s.cfg.Now().Add(s.cfg.WriteTimeout)); err != nil {
				s.close(c, websocket.CloseAbnormalClosure, "ping failed")
				return
			}
		case <-c.done:
			return
		}
	}
}

// enqueue hands a message to the connection's writer; a connection that cannot keep up is closed.
func (s *Server) enqueue(c *conn, msg []byte) {
	if c.queued.Add(int64(len(msg))) > s.cfg.QueueBytes {
		c.queued.Add(-int64(len(msg)))
		go s.close(c, closeSlow, "too slow")
		return
	}
	select {
	case c.send <- msg:
	case <-c.done:
		c.queued.Add(-int64(len(msg)))
	default:
		c.queued.Add(-int64(len(msg)))
		go s.close(c, closeSlow, "too slow")
	}
}

func (s *Server) sendFrame(c *conn, kind wire.Kind, h wire.Header, body []byte) {
	msg, err := wire.Encode(kind, h, body)
	if err != nil {
		return
	}
	s.enqueue(c, msg)
}

func (s *Server) refuse(c *conn, runPid, code string) {
	body, _ := json.Marshal(map[string]string{"code": code})
	s.sendFrame(c, wire.KindError, wire.Header{Run: runPid}, body)
}

func (s *Server) readLoop(c *conn) {
	defer s.close(c, websocket.CloseNormalClosure, "")
	readWindow := 2*s.cfg.PingEvery + s.cfg.WriteTimeout
	c.ws.SetPongHandler(func(string) error {
		return c.ws.SetReadDeadline(s.cfg.Now().Add(readWindow))
	})

	// The first message is hello, within the hello timeout.
	_ = c.ws.SetReadDeadline(s.cfg.Now().Add(s.cfg.HelloTimeout))
	typ, msg, err := c.ws.ReadMessage()
	if err != nil {
		s.closeWith(c, closeNoHello, "no hello")
		return
	}
	frame, err := wire.Decode(msg)
	if typ != websocket.BinaryMessage || err != nil || frame.Kind != wire.KindHello {
		s.closeWith(c, closeNoHello, "no hello")
		return
	}
	if !s.hello(c, frame) {
		return
	}
	_ = c.ws.SetReadDeadline(s.cfg.Now().Add(readWindow))

	for {
		typ, msg, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		_ = c.ws.SetReadDeadline(s.cfg.Now().Add(readWindow))
		frame, err := wire.Decode(msg)
		if typ != websocket.BinaryMessage || err != nil {
			s.closeWith(c, closeProtocol, "not a frame")
			return
		}
		var ok bool
		if c.side == sideAgent {
			ok = s.fromAgent(c, frame, msg)
		} else {
			ok = s.fromBrowser(c, frame, msg)
		}
		if !ok {
			s.closeWith(c, closeProtocol, "unexpected "+frame.Kind.String())
			return
		}
	}
}

func (s *Server) hello(c *conn, frame wire.Frame) bool {
	typ := token.TypeBrowser
	if c.side == sideAgent {
		typ = token.TypeAgent
	}
	claims, err := s.cfg.Keys.Verify(string(frame.Body), typ, s.cfg.Now())
	if err != nil {
		s.log.WithFields(log.Fields{"conn": c.id, "side": c.side.String(), "reason": refusalReason(err)}).Info("hello refused")
		s.refuse(c, "", CodeUnauthorized)
		s.closeWith(c, closeNoHello, "unauthorized")
		return false
	}

	s.mu.Lock()
	c.sub = claims.Sub
	var replaced *conn
	if c.side == sideAgent {
		replaced = s.agents[claims.Sub]
		s.agents[claims.Sub] = c
	} else {
		s.browsers[c] = struct{}{}
	}
	s.mu.Unlock()
	if replaced != nil {
		s.close(replaced, closeReplaced, "replaced by a newer connection")
	}

	max := s.cfg.MaxBrowserFrame
	if c.side == sideAgent {
		max = s.cfg.MaxAgentFrame
	}
	s.sendFrame(c, wire.KindWelcome, wire.Header{Conn: c.id, Max: int(max)}, nil)
	s.log.WithFields(log.Fields{"conn": c.id, "side": c.side.String(), "sub": claims.Sub}).Info("connected")
	return true
}

func refusalReason(err error) string {
	if e, ok := err.(*token.Error); ok {
		return e.Reason
	}
	return err.Error()
}

func (s *Server) fromBrowser(c *conn, frame wire.Frame, msg []byte) bool {
	switch frame.Kind {
	case wire.KindRun:
		s.startRun(c, frame, msg)
	case wire.KindAck, wire.KindCancel:
		s.mu.Lock()
		r := s.runs[frame.Header.Run]
		var agent *conn
		if r != nil && r.browser == c {
			agent = r.agent
		}
		s.mu.Unlock()
		if agent != nil {
			s.enqueue(agent, msg)
		}
	case wire.KindResume:
		s.resume(c, frame, msg)
	default:
		return false
	}
	return true
}

func (s *Server) startRun(c *conn, frame wire.Frame, msg []byte) {
	runPid := frame.Header.Run
	claims, err := s.cfg.Keys.Verify(string(frame.Body), token.TypeTicket, s.cfg.Now())
	if err != nil {
		code := CodeTicketInvalid
		if token.Expired(err) {
			code = CodeTicketExpired
		}
		s.log.WithFields(log.Fields{"conn": c.id, "run": runPid, "reason": refusalReason(err)}).Info("ticket refused")
		s.refuse(c, runPid, code)
		return
	}
	if err := token.CheckTicket(claims); err != nil || claims.Run != runPid {
		reason := "header names another run"
		if err != nil {
			reason = refusalReason(err)
		}
		s.log.WithFields(log.Fields{"conn": c.id, "run": runPid, "reason": reason}).Info("ticket refused")
		s.refuse(c, runPid, CodeTicketInvalid)
		return
	}
	now := s.cfg.Now()

	s.mu.Lock()
	code := ""
	switch {
	case claims.Sub != c.sub:
		code = CodeNotYours
	case s.seen(claims.Jti, now):
		code = CodeTicketInvalid
	case s.runs[runPid] != nil:
		code = CodeTicketInvalid
	case len(c.runs) >= s.cfg.MaxRunsPerConn:
		code = CodeTooManyRuns
	case s.agents[claims.Agt] == nil:
		code = CodeAgentOffline
	}
	if code != "" {
		s.mu.Unlock()
		s.refuse(c, runPid, code)
		return
	}
	s.jtis[claims.Jti] = now.Add(s.cfg.JTIWindow)
	agent := s.agents[claims.Agt]
	r := &run{
		pid: runPid, user: c.sub, agentPid: claims.Agt, agent: agent, browser: c,
		limit: 3*claims.Lim.Bytes + 1<<20, started: now,
	}
	s.runs[runPid] = r
	c.runs[runPid] = r
	agent.runs[runPid] = r
	s.mu.Unlock()

	s.enqueue(agent, msg)
}

// seen reports whether a ticket id was used in the window, pruning old ones. Under s.mu.
func (s *Server) seen(jti string, now time.Time) bool {
	if until, ok := s.jtis[jti]; ok && now.Before(until) {
		return true
	}
	if len(s.jtis) > 4096 {
		for id, until := range s.jtis {
			if !now.Before(until) {
				delete(s.jtis, id)
			}
		}
	}
	return false
}

func (s *Server) resume(c *conn, frame wire.Frame, msg []byte) {
	s.mu.Lock()
	r := s.runs[frame.Header.Run]
	// The run's own person, from any connection: after a network drop the old socket can look open
	// here until its read deadline, longer than the grace, so a resume from a newer connection of
	// the same person takes the run over (the old one gets no more of its frames).
	if r == nil || r.user != c.sub || r.browser == c {
		s.mu.Unlock()
		s.refuse(c, frame.Header.Run, CodeRunUnknown)
		return
	}
	if r.grace != nil {
		r.grace.Stop()
		r.grace = nil
	}
	if r.browser != nil {
		delete(r.browser.runs, r.pid)
	}
	r.browser = c
	c.runs[r.pid] = r
	agent := r.agent
	s.mu.Unlock()
	s.enqueue(agent, msg)
}

func (s *Server) fromAgent(c *conn, frame wire.Frame, msg []byte) bool {
	switch frame.Kind {
	case wire.KindAccepted, wire.KindRejected, wire.KindData:
	default:
		return false
	}
	s.mu.Lock()
	r := s.runs[frame.Header.Run]
	if r == nil || r.agent != c {
		s.mu.Unlock()
		return true // a run that ended (cancelled, gone): its late frames go nowhere
	}
	r.bytes += int64(len(msg))
	r.frames++
	browser := r.browser
	if r.bytes > r.limit {
		s.finish(r, CodeRunTooLarge)
		s.mu.Unlock()
		s.refuse(c, r.pid, CodeRunTooLarge)
		if browser != nil {
			s.refuse(browser, r.pid, CodeRunTooLarge)
		}
		return true
	}
	last := frame.Kind == wire.KindRejected || (frame.Kind == wire.KindData && wire.Final(frame.Header.T))
	if last {
		how := "rejected"
		if frame.Kind == wire.KindData {
			how = frame.Header.T
		}
		s.finish(r, how)
	}
	s.mu.Unlock()
	if browser != nil {
		s.enqueue(browser, msg)
	}
	return true
}

// finish forgets a run. Under s.mu.
func (s *Server) finish(r *run, how string) {
	if s.runs[r.pid] != r {
		return
	}
	delete(s.runs, r.pid)
	if r.grace != nil {
		r.grace.Stop()
		r.grace = nil
	}
	if r.browser != nil {
		delete(r.browser.runs, r.pid)
	}
	if r.agent != nil {
		delete(r.agent.runs, r.pid)
	}
	s.log.WithFields(log.Fields{
		"run": r.pid, "agent": r.agentPid, "user": r.user, "bytes": r.bytes, "frames": r.frames,
		"ms": s.cfg.Now().Sub(r.started).Milliseconds(), "end": how,
	}).Info("run ended")
}

func (s *Server) closeWith(c *conn, code int, reason string) {
	s.close(c, code, reason)
}

// close ends a connection once and settles its runs: a tab's wait for it to come back, an
// agent's end with agent_gone.
func (s *Server) close(c *conn, code int, reason string) {
	c.once.Do(func() {
		_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), s.cfg.Now().Add(time.Second))
		close(c.done)
		_ = c.ws.Close()

		s.mu.Lock()
		c.closed = true
		var notify []func()
		if c.side == sideAgent {
			if s.agents[c.sub] == c {
				delete(s.agents, c.sub)
			}
			for _, r := range c.runs {
				if b := r.browser; b != nil {
					pid := r.pid
					notify = append(notify, func() { s.refuse(b, pid, CodeAgentGone) })
				}
				s.finish(r, CodeAgentGone)
			}
		} else {
			delete(s.browsers, c)
			for _, r := range c.runs {
				r.browser = nil
				rr := r
				rr.grace = time.AfterFunc(s.cfg.Grace, func() { s.graceOver(rr) })
			}
			c.runs = map[string]*run{}
		}
		s.mu.Unlock()
		for _, n := range notify {
			n()
		}
		if c.sub != "" {
			s.log.WithFields(log.Fields{"conn": c.id, "side": c.side.String(), "sub": c.sub, "code": code}).Info("disconnected")
		}
	})
}

// graceOver: the tab did not come back; the agent stops the run.
func (s *Server) graceOver(r *run) {
	s.mu.Lock()
	if s.runs[r.pid] != r || r.browser != nil {
		s.mu.Unlock()
		return
	}
	agent := r.agent
	s.finish(r, "browser_gone")
	s.mu.Unlock()
	if agent != nil {
		s.sendFrame(agent, wire.KindGone, wire.Header{Run: r.pid}, nil)
	}
}

// Stats is what /healthz shows, for tests too.
func (s *Server) Stats() (agents, browsers, runs int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.agents), len(s.browsers), len(s.runs)
}

func (c *conn) String() string { return fmt.Sprintf("%s/%s", c.side, c.id) }
