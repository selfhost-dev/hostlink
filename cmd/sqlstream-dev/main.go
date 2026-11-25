// sqlstream-dev runs the SQL stream path end to end on a developer's machine, without the
// backend: it keeps a test signing key and agent key in a directory, runs the query service
// against a ClickHouse you name, mints the tokens and tickets the backend would, and plays a
// browser tab that times each run. Test keys only: it never sees the backend's.
//
//	sqlstream-dev keys  -dir D                       make the keys; prints SQLRELAY_KEYS
//	sqlstream-dev agent -dir D -relay ws://... -clickhouse http://... -users-d DIR
//	sqlstream-dev mint  -dir D -bpk B -sql S ...     a browser token and a ticket, as JSON
//	sqlstream-dev probe -dir D -relay ws://... -sql S -runs N [-cancel-after 2s]
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"hostlink/app/services/sqlstream"
	"hostlink/internal/crypto"
	"hostlink/internal/sqlstream/e2e"
	"hostlink/internal/sqlstream/token"
	"hostlink/internal/sqlstream/wire"
)

const (
	kid     = "dev"
	agentID = "agt_0000000000000000000000000000dec0"
	userID  = "user_dev"
)

var b64u = base64.RawURLEncoding

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: sqlstream-dev keys|agent|mint|probe [flags]")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keys":
		err = keysCmd(os.Args[2:])
	case "agent":
		err = agentCmd(os.Args[2:])
	case "mint":
		err = mintCmd(os.Args[2:])
	case "probe":
		err = probeCmd(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sqlstream-dev:", err)
		os.Exit(1)
	}
}

type keys struct {
	sign  ed25519.PrivateKey
	agent *rsa.PrivateKey
}

func loadKeys(dir string) (*keys, error) {
	seed, err := os.ReadFile(filepath.Join(dir, "signing.seed"))
	if err != nil {
		return nil, fmt.Errorf("no keys in %s (run sqlstream-dev keys): %w", dir, err)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(seed)))
	if err != nil || len(raw) != ed25519.SeedSize {
		return nil, errors.New("signing.seed is not a base64 Ed25519 seed")
	}
	agent, err := crypto.LoadPrivateKey(filepath.Join(dir, "agent_key.pem"))
	if err != nil {
		return nil, err
	}
	return &keys{sign: ed25519.NewKeyFromSeed(raw), agent: agent}, nil
}

func (k *keys) relayKeys() string {
	return kid + ":" + b64u.EncodeToString(k.sign.Public().(ed25519.PublicKey))
}

func (k *keys) agentSPKI() string {
	der, _ := x509.MarshalPKIXPublicKey(&k.agent.PublicKey)
	return base64.StdEncoding.EncodeToString(der)
}

func (k *keys) token(typ string, c *token.Claims, life time.Duration) string {
	now := time.Now()
	c.Iss, c.Aud, c.Iat, c.Exp = token.Issuer, token.Audience, now.Unix(), now.Add(life).Unix()
	if c.Jti == "" {
		j := make([]byte, 16)
		_, _ = rand.Read(j)
		c.Jti = b64u.EncodeToString(j)
	}
	tok, err := token.Sign(k.sign, kid, typ, c)
	if err != nil {
		panic(err)
	}
	return tok
}

func keysCmd(args []string) error {
	fs := flag.NewFlagSet("keys", flag.ExitOnError)
	dir := fs.String("dir", "", "where to keep the test keys")
	_ = fs.Parse(args)
	if *dir == "" {
		return errors.New("-dir is required")
	}
	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(*dir, "signing.seed")); errors.Is(err, os.ErrNotExist) {
		seed := make([]byte, ed25519.SeedSize)
		_, _ = rand.Read(seed)
		if err := os.WriteFile(filepath.Join(*dir, "signing.seed"), []byte(base64.StdEncoding.EncodeToString(seed)), 0o600); err != nil {
			return err
		}
		agent, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			return err
		}
		pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(agent)})
		if err := os.WriteFile(filepath.Join(*dir, "agent_key.pem"), pemBytes, 0o600); err != nil {
			return err
		}
	}
	k, err := loadKeys(*dir)
	if err != nil {
		return err
	}
	fmt.Println("SQLRELAY_KEYS=" + k.relayKeys())
	fmt.Println("agent_key=" + k.agentSPKI())
	return nil
}

// devAPI is the backend for a standalone agent: a fixed session, outcomes printed.
type devAPI struct {
	session *sqlstream.Session
}

func (a *devAPI) Session(context.Context) (*sqlstream.Session, error) { return a.session, nil }
func (a *devAPI) Outcome(_ context.Context, run string, o sqlstream.Outcome) error {
	b, _ := json.Marshal(map[string]any{"run": run, "outcome": o})
	fmt.Println(string(b))
	return nil
}
func (a *devAPI) PlatformLogin(context.Context) (string, string, error) {
	return os.Getenv("SQLSTREAM_DEV_KILL_USER"), os.Getenv("SQLSTREAM_DEV_KILL_PASSWORD"), nil
}

func agentCmd(args []string) error {
	fs := flag.NewFlagSet("agent", flag.ExitOnError)
	dir := fs.String("dir", "", "the test keys")
	relayURL := fs.String("relay", "ws://127.0.0.1:8090", "the relay")
	clickhouse := fs.String("clickhouse", "http://127.0.0.1:8123", "the database's HTTP port")
	usersD := fs.String("users-d", "/etc/clickhouse-server/users.d", "where the account's file is")
	_ = fs.Parse(args)
	k, err := loadKeys(*dir)
	if err != nil {
		return err
	}
	pub := k.sign.Public().(ed25519.PublicKey)
	api := &devAPI{session: &sqlstream.Session{
		Token:     k.token(token.TypeAgent, &token.Claims{Sub: agentID}, 24*time.Hour),
		RelayURL:  *relayURL,
		Keys:      []token.JWK{{Kid: kid, Kty: "OKP", Crv: "Ed25519", X: b64u.EncodeToString(pub)}},
		ExpiresAt: time.Now().Add(24 * time.Hour),
	}}
	svc := sqlstream.New(sqlstream.Config{AgentID: agentID, PrivateKey: k.agent, API: api, ClickHouseURL: *clickhouse, UsersDir: *usersD})
	fmt.Fprintf(os.Stderr, "agent %s: relay %s, ClickHouse %s, users.d %s\n", agentID, *relayURL, *clickhouse, *usersD)
	svc.Run(context.Background())
	return nil
}

type ticketFlags struct {
	sql, sqlFile, passwordFile, db, user, stamp string
	rows, bytes, secs                           int64
}

func (t *ticketFlags) bind(fs *flag.FlagSet) {
	fs.StringVar(&t.sql, "sql", "", "the statement")
	fs.StringVar(&t.sqlFile, "sql-file", "", "or a file holding it")
	fs.StringVar(&t.passwordFile, "password-file", "", "the account's password, in a file")
	fs.StringVar(&t.db, "db", "default", "the database")
	fs.StringVar(&t.user, "user", "selfhost_read", "the account")
	fs.StringVar(&t.stamp, "stamp", "1", "the account file's sync stamp")
	fs.Int64Var(&t.rows, "rows", 10000, "rows to show")
	fs.Int64Var(&t.bytes, "bytes", 64<<20, "bytes of rows to send")
	fs.Int64Var(&t.secs, "secs", 3600, "seconds to run")
}

func (t *ticketFlags) statement() (string, error) {
	if t.sqlFile != "" {
		b, err := os.ReadFile(t.sqlFile)
		return strings.TrimSpace(string(b)), err
	}
	if t.sql == "" {
		return "", errors.New("-sql or -sql-file is required")
	}
	return t.sql, nil
}

func newRunPid() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return fmt.Sprintf("sqr_%x", b)
}

func (k *keys) ticket(t ticketFlags, runPid, bpk string) (string, error) {
	sql, err := t.statement()
	if err != nil {
		return "", err
	}
	password, err := os.ReadFile(t.passwordFile)
	if err != nil {
		return "", fmt.Errorf("-password-file: %w", err)
	}
	sealed, err := crypto.SealEnv(map[string]string{"SQL": sql, "SQL_ROLE_PASSWORD": strings.TrimSpace(string(password))}, runPid, &k.agent.PublicKey)
	if err != nil {
		return "", err
	}
	kind := "select"
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(sql)), "explain") {
		kind = "explain"
	}
	return k.token(token.TypeTicket, &token.Claims{
		Sub: userID, Agt: agentID, Run: runPid, Qid: runPid + "_1", Ins: "awsinst_dev", Eng: "clickhouse",
		DB: t.db, Mode: "read", Kind: kind, Acct: &token.Account{User: t.user, Stamp: t.stamp},
		Lim: &token.Limits{Rows: t.rows, Bytes: t.bytes, Secs: t.secs}, Bpk: bpk, Sealed: sealed,
	}, 60*time.Second), nil
}

func mintCmd(args []string) error {
	fs := flag.NewFlagSet("mint", flag.ExitOnError)
	dir := fs.String("dir", "", "the test keys")
	bpk := fs.String("bpk", "", "the tab's key for this run (base64url P-256 point)")
	relayURL := fs.String("relay", "ws://127.0.0.1:8090", "the relay")
	var t ticketFlags
	t.bind(fs)
	_ = fs.Parse(args)
	k, err := loadKeys(*dir)
	if err != nil {
		return err
	}
	runPid := newRunPid()
	out := map[string]any{
		"run": runPid, "relay_url": *relayURL, "agent_key": k.agentSPKI(),
		"browser_token": k.token(token.TypeBrowser, &token.Claims{Sub: userID, Org: "org_dev"}, 10*time.Minute),
		"limits":        map[string]int64{"rows": t.rows, "bytes": t.bytes, "secs": t.secs},
	}
	if *bpk != "" {
		if out["ticket"], err = k.ticket(t, runPid, *bpk); err != nil {
			return err
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// probe plays a tab: one connection, runs one after another, each timed.
func probeCmd(args []string) error {
	fs := flag.NewFlagSet("probe", flag.ExitOnError)
	dir := fs.String("dir", "", "the test keys")
	relayURL := fs.String("relay", "ws://127.0.0.1:8090", "the relay")
	origin := fs.String("origin", "http://localhost:54588", "the Origin to send")
	runs := fs.Int("runs", 1, "how many runs")
	cancelAfter := fs.Duration("cancel-after", 0, "send Cancel this long after the run starts")
	quiet := fs.Bool("quiet", false, "print only the summary")
	show := fs.Bool("show-rows", false, "print every row received, one JSON array a line")
	ticketCmd := fs.String("ticket-cmd", "", "a shell command that prints the run's ticket, given BPK and RUN in its environment (the backend's own minting)")
	var t ticketFlags
	t.bind(fs)
	_ = fs.Parse(args)
	k, err := loadKeys(*dir)
	if err != nil {
		return err
	}
	ws, _, err := websocket.DefaultDialer.Dial(strings.TrimRight(*relayURL, "/")+"/v1/browser", http.Header{"Origin": {*origin}})
	if err != nil {
		return err
	}
	defer ws.Close()
	send := func(kind wire.Kind, h wire.Header, body []byte) error {
		msg, _ := wire.Encode(kind, h, body)
		return ws.WriteMessage(websocket.BinaryMessage, msg)
	}
	recv := func() (wire.Frame, error) {
		_ = ws.SetReadDeadline(time.Now().Add(time.Duration(t.secs+30) * time.Second))
		_, msg, err := ws.ReadMessage()
		if err != nil {
			return wire.Frame{}, err
		}
		return wire.Decode(append([]byte(nil), msg...))
	}
	if err := send(wire.KindHello, wire.Header{}, []byte(k.token(token.TypeBrowser, &token.Claims{Sub: userID, Org: "org_dev"}, 10*time.Minute))); err != nil {
		return err
	}
	if f, err := recv(); err != nil || f.Kind != wire.KindWelcome {
		return fmt.Errorf("no welcome: %v", err)
	}

	var firstRows, ends []time.Duration
	for i := 0; i < *runs; i++ {
		priv, bpk, err := e2e.BrowserKey()
		if err != nil {
			return err
		}
		runPid := newRunPid()
		var tk string
		if *ticketCmd != "" {
			cmd := exec.Command("sh", "-c", *ticketCmd)
			cmd.Env = append(os.Environ(), "BPK="+bpk, "RUN="+runPid)
			cmd.Stderr = os.Stderr
			out, err := cmd.Output()
			if err != nil {
				return fmt.Errorf("-ticket-cmd: %w", err)
			}
			lines := strings.Fields(strings.TrimSpace(string(out)))
			if len(lines) == 0 {
				return errors.New("-ticket-cmd printed nothing")
			}
			tk = lines[len(lines)-1]
		} else if tk, err = k.ticket(t, runPid, bpk); err != nil {
			return err
		}
		start := time.Now()
		if err := send(wire.KindRun, wire.Header{Run: runPid}, []byte(tk)); err != nil {
			return err
		}
		var opener *e2e.Opener
		var rows int
		var first, cancelSent time.Duration
		cancelled := false
		for {
			if *cancelAfter > 0 && !cancelled && time.Since(start) >= *cancelAfter {
				cancelled = true
				cancelSent = time.Since(start)
				_ = send(wire.KindCancel, wire.Header{Run: runPid}, nil)
			}
			f, err := recv()
			if err != nil {
				return err
			}
			if f.Kind == wire.KindRejected || f.Kind == wire.KindError {
				return fmt.Errorf("run %s refused: %s", runPid, f.Body)
			}
			if f.Kind == wire.KindAccepted {
				if opener, err = e2e.Open(runPid, priv, bpk, f.Body, &k.agent.PublicKey); err != nil {
					return err
				}
				continue
			}
			plain, err := opener.Open(f.Prefix, *f.Header.Seq, f.Body)
			if err != nil {
				return fmt.Errorf("frame %d does not open: %w", *f.Header.Seq, err)
			}
			_ = send(wire.KindAck, wire.Header{Run: runPid, Seq: f.Header.Seq}, nil)
			switch f.Header.T {
			case wire.TypeRows:
				var b struct{ Rows [][]any }
				_ = json.Unmarshal(plain, &b)
				if rows == 0 {
					first = time.Since(start)
				}
				rows += len(b.Rows)
				if *show {
					for _, r := range b.Rows {
						line, _ := json.Marshal(r)
						fmt.Println("ROW " + string(line))
					}
				}
			case wire.TypeProgress:
				if !*quiet {
					fmt.Printf("  progress %s\n", plain)
				}
			case wire.TypeColumns:
				if !*quiet {
					fmt.Printf("  columns %s\n", plain)
				}
			}
			if wire.Final(f.Header.T) {
				end := time.Since(start)
				if !*quiet || *runs == 1 {
					fmt.Printf("run %s: first rows %v, %s after %v, %d rows: %s\n", runPid, first.Round(time.Millisecond), f.Header.T, end.Round(time.Millisecond), rows, plain)
				}
				if cancelled {
					fmt.Printf("  cancel sent at %v, run ended %v later\n", cancelSent.Round(time.Millisecond), (end - cancelSent).Round(time.Millisecond))
				}
				if first > 0 {
					firstRows = append(firstRows, first)
				}
				ends = append(ends, end)
				break
			}
		}
	}
	if *runs > 1 {
		fmt.Printf("%d runs: first rows p50 %v p95 %v; end p50 %v p95 %v\n", *runs,
			pct(firstRows, 50), pct(firstRows, 95), pct(ends, 50), pct(ends, 95))
	}
	return nil
}

func pct(d []time.Duration, p int) time.Duration {
	if len(d) == 0 {
		return 0
	}
	s := append([]time.Duration(nil), d...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	i := (len(s)*p + 99) / 100
	if i < 1 {
		i = 1
	}
	return s[i-1].Round(time.Millisecond)
}
