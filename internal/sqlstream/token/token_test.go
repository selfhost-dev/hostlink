package token

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

var now = time.Unix(1795560030, 0)

func keyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

func ticketClaims() *Claims {
	return &Claims{
		Iss: Issuer, Aud: Audience, Sub: "user_1", Iat: now.Unix() - 5, Exp: now.Unix() + 55, Jti: "j1",
		Agt: "agt_8d91f075d45f41da07fa7983a8367941", Run: "sqr_0123456789abcdef0123", Qid: "sqr_0123456789abcdef0123_1",
		Ins: "awsinst_1", Eng: "clickhouse", DB: "analytics", Mode: "read", Kind: "select",
		Acct: &Account{User: "selfhost_read", Stamp: "1795559413440266"},
		Lim:  &Limits{Rows: 10000, Bytes: 64 << 20, Secs: 3600},
		Bpk:  "BAAA", Sealed: "{}",
	}
}

func TestVerify(t *testing.T) {
	pub, priv := keyPair(t)
	keys := NewKeySet(map[string]ed25519.PublicKey{"k1": pub})
	tok, err := Sign(priv, "k1", TypeTicket, ticketClaims())
	if err != nil {
		t.Fatal(err)
	}
	c, err := keys.Verify(tok, TypeTicket, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckTicket(c); err != nil {
		t.Fatal(err)
	}
	if c.Lim.Rows != 10000 || c.Acct.User != "selfhost_read" {
		t.Fatalf("claims %+v", c)
	}
}

func TestVerifyRefuses(t *testing.T) {
	pub, priv := keyPair(t)
	_, otherPriv := keyPair(t)
	keys := NewKeySet(map[string]ed25519.PublicKey{"k1": pub})
	sign := func(c *Claims, kid, typ string, key ed25519.PrivateKey) string {
		tok, err := Sign(key, kid, typ, c)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	with := func(f func(*Claims)) *Claims {
		c := ticketClaims()
		f(c)
		return c
	}
	good := sign(ticketClaims(), "k1", TypeTicket, priv)
	parts := strings.Split(good, ".")

	cases := map[string]struct {
		tok    string
		typ    string
		reason string
	}{
		"two parts":       {parts[0] + "." + parts[1], TypeTicket, "not three parts"},
		"other key":       {sign(ticketClaims(), "k1", TypeTicket, otherPriv), TypeTicket, "bad signature"},
		"unknown kid":     {sign(ticketClaims(), "k9", TypeTicket, priv), TypeTicket, "unknown key id"},
		"other type":      {good, TypeBrowser, "type"},
		"expired":         {sign(with(func(c *Claims) { c.Exp = now.Unix() - 6 }), "k1", TypeTicket, priv), TypeTicket, "expired"},
		"from the future": {sign(with(func(c *Claims) { c.Iat = now.Unix() + 60 }), "k1", TypeTicket, priv), TypeTicket, "future"},
		"other audience":  {sign(with(func(c *Claims) { c.Aud = "api" }), "k1", TypeTicket, priv), TypeTicket, "audience"},
		"other issuer":    {sign(with(func(c *Claims) { c.Iss = "x" }), "k1", TypeTicket, priv), TypeTicket, "issuer"},
		"no subject":      {sign(with(func(c *Claims) { c.Sub = "" }), "k1", TypeTicket, priv), TypeTicket, "subject"},
		"payload swapped": {parts[0] + "." + base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"selfhost"}`)) + "." + parts[2], TypeTicket, "bad signature"},
	}
	for name, c := range cases {
		_, err := keys.Verify(c.tok, c.typ, now)
		if err == nil || !strings.Contains(err.Error(), c.reason) {
			t.Errorf("%s: %v, want %q", name, err, c.reason)
		}
	}
	if _, err := keys.Verify(sign(with(func(c *Claims) { c.Exp = now.Unix() - 6 }), "k1", TypeTicket, priv), TypeTicket, now); !Expired(err) {
		t.Errorf("Expired(%v) = false", err)
	}
	// Within the leeway, a token just past its exp still passes.
	if _, err := keys.Verify(sign(with(func(c *Claims) { c.Exp = now.Unix() - 2 }), "k1", TypeTicket, priv), TypeTicket, now); err != nil {
		t.Errorf("2 s past exp: %v", err)
	}
	// An alg other than EdDSA.
	h := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","kid":"k1","typ":"sqls-ticket"}`))
	if _, err := keys.Verify(h+"."+parts[1]+"."+parts[2], TypeTicket, now); err == nil || !strings.Contains(err.Error(), "alg") {
		t.Errorf("alg none: %v", err)
	}
}

func TestCheckTicket(t *testing.T) {
	cases := map[string]func(*Claims){
		"run":         func(c *Claims) { c.Run = "sqr_x" },
		"query id":    func(c *Claims) { c.Qid = "sqr_ffffffffffffffffffff_1" },
		"agent":       func(c *Claims) { c.Agt = "agent" },
		"database":    func(c *Claims) { c.DB = "a;b" },
		"mode":        func(c *Claims) { c.Mode = "write" },
		"kind":        func(c *Claims) { c.Kind = "insert" },
		"account":     func(c *Claims) { c.Acct = &Account{User: "x", Stamp: "12a"} },
		"no account":  func(c *Claims) { c.Acct = nil },
		"rows":        func(c *Claims) { c.Lim.Rows = MaxTicketRows + 1 },
		"secs":        func(c *Claims) { c.Lim.Secs = 0 },
		"bytes":       func(c *Claims) { c.Lim.Bytes = MaxTicketBytes + 1 },
		"browser key": func(c *Claims) { c.Bpk = "" },
		"sealed part": func(c *Claims) { c.Sealed = "" },
		"lifetime":    func(c *Claims) { c.Exp = c.Iat + 3600 },
	}
	for name, mutate := range cases {
		c := ticketClaims()
		mutate(c)
		if err := CheckTicket(c); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseKeys(t *testing.T) {
	pub, _ := keyPair(t)
	enc := base64.RawURLEncoding.EncodeToString(pub)
	keys, err := ParseKeys("k1:" + enc + ", k2:" + enc + "=")
	if err != nil || len(keys) != 2 {
		t.Fatalf("%v %v", keys, err)
	}
	for _, bad := range []string{"", "k1", ":" + enc, "k1:AAAA"} {
		if _, err := ParseKeys(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	jwks := `{"keys":[{"kid":"k1","kty":"OKP","crv":"Ed25519","x":"` + enc + `"},{"kid":"r","kty":"RSA"}]}`
	got, err := ParseJWKS([]byte(jwks))
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
}
