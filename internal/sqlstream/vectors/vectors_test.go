// The SQL stream path's test vectors (sqlstream_vectors.json): fixed keys, a ticket, a key
// exchange and three sealed frames, which the backend's and the console's tests also open, so
// the three implementations agree byte for byte. SQLSTREAM_WRITE_VECTORS=1 writes them again
// (the accepted signature is randomized, so it changes); every other run checks them.
package vectors

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"hostlink/internal/sqlstream/e2e"
	"hostlink/internal/sqlstream/token"
	"hostlink/internal/sqlstream/wire"
)

var (
	b64u = base64.RawURLEncoding
	b64  = base64.StdEncoding
)

type Frame struct {
	Seq       uint64 `json:"seq"`
	T         string `json:"t"`
	Prefix    string `json:"prefix_b64"`
	Plaintext string `json:"plaintext"`
	Sealed    string `json:"sealed_b64"`
	Message   string `json:"message_b64"`
}

type File struct {
	Comment string `json:"comment"`
	Ed25519 struct {
		Seed   string `json:"seed_b64u"`
		Public string `json:"public_b64u"`
		Kid    string `json:"kid"`
	} `json:"ed25519"`
	Ticket struct {
		Payload string        `json:"payload_json"`
		Token   string        `json:"token"`
		Claims  *token.Claims `json:"claims"`
		Now     int64         `json:"verify_at"`
	} `json:"ticket"`
	E2E struct {
		Run            string  `json:"run"`
		BrowserPrivate string  `json:"browser_private_b64u"`
		BrowserPublic  string  `json:"browser_public_b64u"`
		AgentPrivate   string  `json:"agent_private_b64u"`
		AgentPublic    string  `json:"agent_public_b64u"`
		Secret         string  `json:"secret_b64u"`
		Key            string  `json:"key_b64u"`
		AgentRSASPKI   string  `json:"agent_rsa_spki_b64"`
		AcceptedSigned string  `json:"accepted_message"`
		AcceptedBody   string  `json:"accepted_body"`
		Frames         []Frame `json:"frames"`
	} `json:"e2e"`
}

const (
	vectorsPath = "../testdata/sqlstream_vectors.json"
	rsaKeyPath  = "../testdata/agent_rsa_test_key.pem"
	run         = "sqr_0123456789abcdef0123"
	kid         = "test-2026-11"
)

func seed() []byte {
	s := make([]byte, ed25519.SeedSize)
	for i := range s {
		s[i] = byte(i)
	}
	return s
}

func scalar(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func loadRSA(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	data, err := os.ReadFile(rsaKeyPath)
	if os.IsNotExist(err) && os.Getenv("SQLSTREAM_WRITE_VECTORS") == "1" {
		key, genErr := rsa.GenerateKey(rand.Reader, 2048)
		if genErr != nil {
			t.Fatal(genErr)
		}
		data = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
		if err := os.WriteFile(rsaKeyPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
	} else if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// The frames the vectors seal, in order.
var plaintexts = []struct {
	t    string
	body string
}{
	{wire.TypeColumns, `{"columns":[{"name":"event","type":"LowCardinality(String)"},{"name":"n","type":"UInt64"}]}`},
	{wire.TypeRows, `{"rows":[["click","18446744073709551615"],["view",null],["café","0"]]}`},
	{wire.TypeEnd, `{"status":"completed","rows":3,"truncated":false,"reason":null,"elapsed_ms":12,"read_rows":3,"read_bytes":48,"memory_bytes":1024}`},
}

func write(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(seed())
	agentRSA := loadRSA(t)

	var f File
	f.Comment = "sql-stream-v1 test vectors: written by hostlink internal/sqlstream/vectors (SQLSTREAM_WRITE_VECTORS=1); every implementation's tests open them. Test keys only."
	f.Ed25519.Seed = b64u.EncodeToString(seed())
	f.Ed25519.Public = b64u.EncodeToString(priv.Public().(ed25519.PublicKey))
	f.Ed25519.Kid = kid

	browser, err := ecdh.P256().NewPrivateKey(scalar(0x11))
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ecdh.P256().NewPrivateKey(scalar(0x22))
	if err != nil {
		t.Fatal(err)
	}
	bpk := b64u.EncodeToString(browser.PublicKey().Bytes())
	apk := b64u.EncodeToString(agent.PublicKey().Bytes())

	sealedPart := `{"v":1,"alg":"RSA-OAEP-256+A256GCM","ek":"","iv":"","ct":""}`
	claims := &token.Claims{
		Iss: token.Issuer, Aud: token.Audience, Sub: "user_7925448ae619be7c425e893bd84658fe",
		Iat: 1795560000, Exp: 1795560060, Jti: "AAECAwQFBgcICQoLDA0ODw",
		Agt: "agt_8d91f075d45f41da07fa7983a8367941", Run: run, Qid: run + "_1",
		Ins: "awsinst_cb2e6006f5566529d395", Eng: "clickhouse", DB: "analytics", Mode: "read", Kind: "select",
		Acct:   &token.Account{User: "selfhost_read", Stamp: "1795559413440266"},
		Lim:    &token.Limits{Rows: 10000, Bytes: 67108864, Secs: 3600},
		Bpk:    bpk,
		Sealed: sealedPart,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	f.Ticket.Payload = string(payload)
	f.Ticket.Token = token.SignPayload(priv, kid, token.TypeTicket, payload)
	f.Ticket.Claims = claims
	f.Ticket.Now = 1795560030

	secret, err := agent.ECDH(browser.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	key, err := e2e.DeriveKey(secret, run)
	if err != nil {
		t.Fatal(err)
	}
	spki, err := x509.MarshalPKIXPublicKey(&agentRSA.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	accepted, sealer, err := e2e.AcceptWith(agent, run, bpk, agentRSA)
	if err != nil {
		t.Fatal(err)
	}

	f.E2E.Run = run
	f.E2E.BrowserPrivate = b64u.EncodeToString(browser.Bytes())
	f.E2E.BrowserPublic = bpk
	f.E2E.AgentPrivate = b64u.EncodeToString(agent.Bytes())
	f.E2E.AgentPublic = apk
	f.E2E.Secret = b64u.EncodeToString(secret)
	f.E2E.Key = b64u.EncodeToString(key)
	f.E2E.AgentRSASPKI = b64.EncodeToString(spki)
	f.E2E.AcceptedSigned = string(e2e.AcceptedMessage(run, apk, bpk))
	f.E2E.AcceptedBody = string(accepted)
	for i, p := range plaintexts {
		seq := uint64(i + 1)
		prefix, err := wire.Prefix(wire.KindData, wire.Header{Run: run, Seq: wire.SeqOf(seq), T: p.t})
		if err != nil {
			t.Fatal(err)
		}
		sealed := sealer.Seal(prefix, seq, []byte(p.body))
		f.E2E.Frames = append(f.E2E.Frames, Frame{
			Seq: seq, T: p.t, Prefix: b64.EncodeToString(prefix), Plaintext: p.body,
			Sealed: b64.EncodeToString(sealed), Message: b64.EncodeToString(append(append([]byte{}, prefix...), sealed...)),
		})
	}

	out, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vectorsPath, append(out, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestVectors(t *testing.T) {
	if os.Getenv("SQLSTREAM_WRITE_VECTORS") == "1" {
		if err := os.MkdirAll(filepath.Dir(vectorsPath), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t)
	}
	data, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var f File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}

	// The key: the seed gives the public key.
	rawSeed, _ := b64u.DecodeString(f.Ed25519.Seed)
	priv := ed25519.NewKeyFromSeed(rawSeed)
	if got := b64u.EncodeToString(priv.Public().(ed25519.PublicKey)); got != f.Ed25519.Public {
		t.Fatalf("public key %s, want %s", got, f.Ed25519.Public)
	}

	// The ticket: signing its exact payload gives its exact token, which verifies.
	if got := token.SignPayload(priv, f.Ed25519.Kid, token.TypeTicket, []byte(f.Ticket.Payload)); got != f.Ticket.Token {
		t.Fatalf("ticket does not sign the same:\n got %s\nwant %s", got, f.Ticket.Token)
	}
	keys := token.NewKeySet(map[string]ed25519.PublicKey{f.Ed25519.Kid: priv.Public().(ed25519.PublicKey)})
	claims, err := keys.Verify(f.Ticket.Token, token.TypeTicket, time.Unix(f.Ticket.Now, 0))
	if err != nil {
		t.Fatal(err)
	}
	if err := token.CheckTicket(claims); err != nil {
		t.Fatal(err)
	}
	if claims.Run != f.E2E.Run || claims.Bpk != f.E2E.BrowserPublic || claims.Lim.Rows != 10000 {
		t.Fatalf("claims %+v", claims)
	}
	if _, err := keys.Verify(f.Ticket.Token, token.TypeTicket, time.Unix(f.Ticket.Now+3600, 0)); !token.Expired(err) {
		t.Fatalf("an hour later: %v, want expired", err)
	}

	// The key exchange: both private keys give the secret, the secret the key.
	rawB, _ := b64u.DecodeString(f.E2E.BrowserPrivate)
	rawA, _ := b64u.DecodeString(f.E2E.AgentPrivate)
	browser, err := ecdh.P256().NewPrivateKey(rawB)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := ecdh.P256().NewPrivateKey(rawA)
	if err != nil {
		t.Fatal(err)
	}
	s1, _ := browser.ECDH(agent.PublicKey())
	s2, _ := agent.ECDH(browser.PublicKey())
	if b64u.EncodeToString(s1) != f.E2E.Secret || !bytes.Equal(s1, s2) {
		t.Fatalf("secret differs")
	}
	key, _ := e2e.DeriveKey(s1, f.E2E.Run)
	if b64u.EncodeToString(key) != f.E2E.Key {
		t.Fatalf("key %s, want %s", b64u.EncodeToString(key), f.E2E.Key)
	}

	// accepted: its signature verifies with the agent's RSA key; the opener opens every frame.
	spki, _ := b64.DecodeString(f.E2E.AgentRSASPKI)
	pub, err := x509.ParsePKIXPublicKey(spki)
	if err != nil {
		t.Fatal(err)
	}
	if string(e2e.AcceptedMessage(f.E2E.Run, f.E2E.AgentPublic, f.E2E.BrowserPublic)) != f.E2E.AcceptedSigned {
		t.Fatal("accepted message differs")
	}
	opener, err := e2e.Open(f.E2E.Run, browser, f.E2E.BrowserPublic, []byte(f.E2E.AcceptedBody), pub.(*rsa.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	for _, fr := range f.E2E.Frames {
		msg, _ := b64.DecodeString(fr.Message)
		frame, err := wire.Decode(msg)
		if err != nil {
			t.Fatal(err)
		}
		if frame.Kind != wire.KindData || frame.Header.Run != f.E2E.Run || *frame.Header.Seq != fr.Seq || frame.Header.T != fr.T {
			t.Fatalf("frame %d header %+v", fr.Seq, frame.Header)
		}
		prefix, _ := b64.DecodeString(fr.Prefix)
		if !bytes.Equal(prefix, frame.Prefix) {
			t.Fatalf("frame %d prefix differs", fr.Seq)
		}
		plain, err := opener.Open(frame.Prefix, fr.Seq, frame.Body)
		if err != nil {
			t.Fatalf("frame %d: %v", fr.Seq, err)
		}
		if string(plain) != fr.Plaintext {
			t.Fatalf("frame %d plaintext %s", fr.Seq, plain)
		}
		// Sealing is deterministic for a key, nonce and prefix: the vector's bytes are the cipher's.
		block, _ := e2e.NewSealerForKey(key)
		if got := b64.EncodeToString(block.Seal(prefix, fr.Seq, []byte(fr.Plaintext))); got != fr.Sealed {
			t.Fatalf("frame %d seals to %s", fr.Seq, got)
		}
	}
}
