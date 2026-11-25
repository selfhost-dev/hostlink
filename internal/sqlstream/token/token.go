// Package token reads and makes the SQL stream path's signed tokens: compact JWS with Ed25519
// (alg EdDSA), signed by the selfhost backend. An agent token lets an agent's connection into
// the relay, a browser token a person's tab, and a ticket one run. Contract: the backend's
// docs/sql-stream-protocol.md, section 1.
package token

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	TypeAgent   = "sqls-agent"
	TypeBrowser = "sqls-browser"
	TypeTicket  = "sqls-ticket"

	Issuer   = "selfhost"
	Audience = "sqlrelay"

	// Leeway is how far clocks may disagree.
	Leeway = 5 * time.Second
)

var b64 = base64.RawURLEncoding

// Claims are every claim the three types carry; each uses its own.
type Claims struct {
	Iss string `json:"iss"`
	Aud string `json:"aud"`
	Sub string `json:"sub"`
	Iat int64  `json:"iat"`
	Exp int64  `json:"exp"`
	Jti string `json:"jti"`

	// A browser token's organisation.
	Org string `json:"org,omitempty"`

	// A ticket's.
	Agt    string   `json:"agt,omitempty"`
	Run    string   `json:"run,omitempty"`
	Qid    string   `json:"qid,omitempty"`
	Ins    string   `json:"ins,omitempty"`
	Eng    string   `json:"eng,omitempty"`
	DB     string   `json:"db,omitempty"`
	Mode   string   `json:"mode,omitempty"`
	Kind   string   `json:"kind,omitempty"`
	Acct   *Account `json:"acct,omitempty"`
	Lim    *Limits  `json:"lim,omitempty"`
	Bpk    string   `json:"bpk,omitempty"`
	Sealed string   `json:"sealed,omitempty"`
}

// Account is the database account a ticket runs as, and the sync its file must name.
type Account struct {
	User  string `json:"user"`
	Stamp string `json:"stamp"`
}

// Limits are a run's: rows to show, bytes of rows to send, seconds to run.
type Limits struct {
	Rows  int64 `json:"rows"`
	Bytes int64 `json:"bytes"`
	Secs  int64 `json:"secs"`
}

type header struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// Error is every refusal. Reason is for the verifier's log; callers answer with one code
// whatever it says.
type Error struct{ Reason string }

func (e *Error) Error() string { return "token refused: " + e.Reason }

func refuse(format string, args ...any) error { return &Error{Reason: fmt.Sprintf(format, args...)} }

// KeySet holds the backend's public keys by key id. Safe to use from many goroutines, and
// replaced whole when the relay reads the keys again.
type KeySet struct {
	mu   sync.RWMutex
	keys map[string]ed25519.PublicKey
}

// NewKeySet returns a set of the given keys.
func NewKeySet(keys map[string]ed25519.PublicKey) *KeySet {
	ks := &KeySet{}
	ks.Replace(keys)
	return ks
}

// Replace swaps every key for the given ones.
func (ks *KeySet) Replace(keys map[string]ed25519.PublicKey) {
	copied := make(map[string]ed25519.PublicKey, len(keys))
	for kid, key := range keys {
		copied[kid] = key
	}
	ks.mu.Lock()
	ks.keys = copied
	ks.mu.Unlock()
}

// Len is how many keys the set holds.
func (ks *KeySet) Len() int {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	return len(ks.keys)
}

func (ks *KeySet) key(kid string) (ed25519.PublicKey, bool) {
	ks.mu.RLock()
	defer ks.mu.RUnlock()
	key, ok := ks.keys[kid]
	return key, ok
}

// ParseKeys reads "kid:<b64u public key>,kid2:..." (SQLRELAY_KEYS, SQL_STREAM_PREVIOUS_KEYS).
func ParseKeys(spec string) (map[string]ed25519.PublicKey, error) {
	keys := map[string]ed25519.PublicKey{}
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		kid, encoded, ok := strings.Cut(item, ":")
		if !ok || kid == "" {
			return nil, fmt.Errorf("key %q is not kid:key", item)
		}
		raw, err := b64.DecodeString(strings.TrimRight(encoded, "="))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("key %s is not a base64url Ed25519 public key", kid)
		}
		keys[kid] = ed25519.PublicKey(raw)
	}
	if len(keys) == 0 {
		return nil, errors.New("no keys")
	}
	return keys, nil
}

// JWK is one key as the backend publishes it (GET /api/v1/sql_stream/keys).
type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
}

// ParseJWKS reads {"keys":[...]}, keeping the Ed25519 ones.
func ParseJWKS(data []byte) (map[string]ed25519.PublicKey, error) {
	var doc struct {
		Keys []JWK `json:"keys"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("keys are not JSON: %w", err)
	}
	keys := map[string]ed25519.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "OKP" || k.Crv != "Ed25519" || k.Kid == "" {
			continue
		}
		raw, err := b64.DecodeString(strings.TrimRight(k.X, "="))
		if err != nil || len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("key %s is not an Ed25519 public key", k.Kid)
		}
		keys[k.Kid] = ed25519.PublicKey(raw)
	}
	if len(keys) == 0 {
		return nil, errors.New("no Ed25519 keys")
	}
	return keys, nil
}

// Verify checks a token of the given type, in the order the contract lists, and returns its claims.
func (ks *KeySet) Verify(tok, typ string, now time.Time) (*Claims, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return nil, refuse("not three parts")
	}
	rawHeader, err := b64.DecodeString(parts[0])
	if err != nil {
		return nil, refuse("header is not base64url")
	}
	var h header
	if err := json.Unmarshal(rawHeader, &h); err != nil {
		return nil, refuse("header is not JSON")
	}
	if h.Alg != "EdDSA" {
		return nil, refuse("alg %q", h.Alg)
	}
	key, ok := ks.key(h.Kid)
	if !ok {
		return nil, refuse("unknown key id %q", h.Kid)
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || len(sig) != ed25519.SignatureSize {
		return nil, refuse("signature is not base64url Ed25519")
	}
	if !ed25519.Verify(key, []byte(parts[0]+"."+parts[1]), sig) {
		return nil, refuse("bad signature")
	}
	if h.Typ != typ {
		return nil, refuse("type %q, want %q", h.Typ, typ)
	}
	rawClaims, err := b64.DecodeString(parts[1])
	if err != nil {
		return nil, refuse("payload is not base64url")
	}
	var c Claims
	if err := json.Unmarshal(rawClaims, &c); err != nil {
		return nil, refuse("payload is not JSON")
	}
	if c.Iss != Issuer {
		return nil, refuse("issuer %q", c.Iss)
	}
	if c.Aud != Audience {
		return nil, refuse("audience %q", c.Aud)
	}
	if c.Exp == 0 || !now.Before(time.Unix(c.Exp, 0).Add(Leeway)) {
		return nil, refuse("expired")
	}
	if c.Iat == 0 || time.Unix(c.Iat, 0).After(now.Add(Leeway)) {
		return nil, refuse("issued in the future")
	}
	if c.Sub == "" || c.Jti == "" {
		return nil, refuse("no subject or id")
	}
	return &c, nil
}

// Expired reports whether a refusal was only the token's age, for the codes that tell them
// apart (ticket_expired).
func Expired(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Reason == "expired"
}

// Sign makes a token: the backend's half, here for tests and the developer tool.
func Sign(priv ed25519.PrivateKey, kid, typ string, claims any) (string, error) {
	rawClaims, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	return SignPayload(priv, kid, typ, rawClaims), nil
}

// SignPayload signs claims given as their exact JSON bytes.
func SignPayload(priv ed25519.PrivateKey, kid, typ string, payload []byte) string {
	rawHeader, _ := json.Marshal(header{Alg: "EdDSA", Kid: kid, Typ: typ})
	signing := b64.EncodeToString(rawHeader) + "." + b64.EncodeToString(payload)
	return signing + "." + b64.EncodeToString(ed25519.Sign(priv, []byte(signing)))
}

var (
	runPattern     = regexp.MustCompile(`^sqr_[0-9a-f]{20}$`)
	qidPattern     = regexp.MustCompile(`^sqr_[0-9a-f]{20}_[1-9][0-9]{0,8}$`)
	agentPattern   = regexp.MustCompile(`^agt_[0-9a-f]{16,64}$`)
	namePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,62}$`)
	stampPattern   = regexp.MustCompile(`^[0-9]{1,18}$`)
	readKinds      = map[string]bool{"select": true, "explain": true, "show": true, "describe": true, "exists": true}
	maxTicketLife  = 120 * time.Second
	MaxTicketRows  = int64(100_000)
	MaxTicketBytes = int64(256 << 20)
	MaxTicketSecs  = int64(3600)
)

// CheckTicket checks a verified ticket's claims are a run this version can take.
func CheckTicket(c *Claims) error {
	switch {
	case !runPattern.MatchString(c.Run):
		return refuse("run")
	case !qidPattern.MatchString(c.Qid) || !strings.HasPrefix(c.Qid, c.Run+"_"):
		return refuse("query id")
	case !agentPattern.MatchString(c.Agt):
		return refuse("agent")
	case c.Eng == "":
		return refuse("engine")
	case !namePattern.MatchString(c.DB):
		return refuse("database")
	case c.Mode != "read":
		return refuse("mode %q", c.Mode)
	case !readKinds[c.Kind]:
		return refuse("kind %q", c.Kind)
	case c.Acct == nil || !namePattern.MatchString(c.Acct.User) || !stampPattern.MatchString(c.Acct.Stamp):
		return refuse("account")
	case c.Lim == nil || c.Lim.Rows < 1 || c.Lim.Rows > MaxTicketRows || c.Lim.Bytes < 1 ||
		c.Lim.Bytes > MaxTicketBytes || c.Lim.Secs < 1 || c.Lim.Secs > MaxTicketSecs:
		return refuse("limits")
	case c.Bpk == "":
		return refuse("browser key")
	case c.Sealed == "":
		return refuse("sealed part")
	case time.Duration(c.Exp-c.Iat)*time.Second > maxTicketLife:
		return refuse("lifetime")
	}
	return nil
}
