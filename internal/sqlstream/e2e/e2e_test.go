package e2e

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"testing"

	"hostlink/internal/sqlstream/wire"
)

const run = "sqr_0123456789abcdef0123"

func rsaKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func prefix(t *testing.T, seq uint64, typ string) []byte {
	t.Helper()
	p, err := wire.Prefix(wire.KindData, wire.Header{Run: run, Seq: wire.SeqOf(seq), T: typ})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestAcceptAndOpen(t *testing.T) {
	agentKey := rsaKey(t)
	browser, bpk, err := BrowserKey()
	if err != nil {
		t.Fatal(err)
	}
	accepted, sealer, err := Accept(run, bpk, agentKey)
	if err != nil {
		t.Fatal(err)
	}
	opener, err := Open(run, browser, bpk, accepted, &agentKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	for seq := uint64(1); seq <= 3; seq++ {
		p := prefix(t, seq, wire.TypeRows)
		sealed := sealer.Seal(p, seq, []byte(`{"rows":[["1"]]}`))
		plain, err := opener.Open(p, seq, sealed)
		if err != nil || string(plain) != `{"rows":[["1"]]}` {
			t.Fatalf("seq %d: %q %v", seq, plain, err)
		}
	}
}

func TestOpenRefuses(t *testing.T) {
	agentKey := rsaKey(t)
	browser, bpk, _ := BrowserKey()
	accepted, sealer, err := Accept(run, bpk, agentKey)
	if err != nil {
		t.Fatal(err)
	}

	// A relay that swaps the agent's key for its own, signed by a key that is not the agent's.
	impostor := rsaKey(t)
	forged, _, _ := Accept(run, bpk, impostor)
	if _, err := Open(run, browser, bpk, forged, &agentKey.PublicKey); !errors.Is(err, ErrNotTheAgent) {
		t.Fatalf("forged accepted: %v", err)
	}
	// The signature names the run and the browser's key: another run's accepted does not open.
	if _, err := Open("sqr_ffffffffffffffffffff", browser, bpk, accepted, &agentKey.PublicKey); !errors.Is(err, ErrNotTheAgent) {
		t.Fatalf("another run: %v", err)
	}
	var a Accepted
	_ = json.Unmarshal(accepted, &a)
	a.APK = bpk
	swapped, _ := json.Marshal(a)
	if _, err := Open(run, browser, bpk, swapped, &agentKey.PublicKey); !errors.Is(err, ErrNotTheAgent) {
		t.Fatalf("key swapped under the signature: %v", err)
	}

	opener, err := Open(run, browser, bpk, accepted, &agentKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	p := prefix(t, 1, wire.TypeRows)
	sealed := sealer.Seal(p, 1, []byte("rows"))
	if _, err := opener.Open(prefix(t, 1, wire.TypeEnd), 1, sealed); err == nil {
		t.Fatal("a header changed under its body opened")
	}
	if _, err := opener.Open(prefix(t, 2, wire.TypeRows), 2, sealed); err == nil {
		t.Fatal("a frame replayed at another seq opened")
	}
	sealed[0] ^= 1
	if _, err := opener.Open(p, 1, sealed); err == nil {
		t.Fatal("a flipped bit opened")
	}
}

func TestParsePublicKey(t *testing.T) {
	_, bpk, _ := BrowserKey()
	if _, err := ParsePublicKey(bpk); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "!!", "BAAA", bpk[:len(bpk)-4]} {
		if _, err := ParsePublicKey(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
	if _, _, err := Accept(run, "BAAA", rsaKey(t)); err == nil {
		t.Fatal("accepted a bad browser key")
	}
}
