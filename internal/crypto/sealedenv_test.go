package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func TestSealedEnvRoundTrip(t *testing.T) {
	key := testKey(t)
	env := map[string]string{"TLS_KEY_PEM": "-----BEGIN PRIVATE KEY-----\nabc\n-----END PRIVATE KEY-----", "EMPTY": ""}

	sealed, err := SealEnv(env, "task_123", &key.PublicKey)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if strings.Contains(sealed, "BEGIN PRIVATE KEY") {
		t.Fatal("the envelope carries the plaintext")
	}

	opened, err := OpenSealedEnv(sealed, "task_123", key)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(opened) != 2 || opened["TLS_KEY_PEM"] != env["TLS_KEY_PEM"] || opened["EMPTY"] != "" {
		t.Fatalf("opened %v, want %v", opened, env)
	}
}

func TestSealedEnvOpensOnlyForItsTask(t *testing.T) {
	key := testKey(t)
	sealed, _ := SealEnv(map[string]string{"A": "1"}, "task_123", &key.PublicKey)

	if _, err := OpenSealedEnv(sealed, "task_456", key); err == nil {
		t.Fatal("an envelope sealed for one task opened for another")
	}
}

func TestSealedEnvOpensOnlyWithTheAgentKey(t *testing.T) {
	sealed, _ := SealEnv(map[string]string{"A": "1"}, "task_123", &testKey(t).PublicKey)

	if _, err := OpenSealedEnv(sealed, "task_123", testKey(t)); err == nil {
		t.Fatal("another agent's key opened the envelope")
	}
}

func TestSealedEnvRejectsTampering(t *testing.T) {
	key := testKey(t)
	sealed, _ := SealEnv(map[string]string{"A": "1"}, "task_123", &key.PublicKey)
	var envelope sealedEnvelope
	if err := json.Unmarshal([]byte(sealed), &envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	data, _ := base64.StdEncoding.DecodeString(envelope.Data)
	data[0] ^= 0xff
	envelope.Data = base64.StdEncoding.EncodeToString(data)
	tampered, _ := json.Marshal(envelope)

	if _, err := OpenSealedEnv(string(tampered), "task_123", key); err == nil {
		t.Fatal("a tampered envelope opened")
	}
}

func TestSealedEnvRefusesUnknownFormatsAndNames(t *testing.T) {
	key := testKey(t)
	for name, sealed := range map[string]string{
		"not json":        "nope",
		"unknown version": `{"v":2,"alg":"RSA-OAEP-256+A256GCM","ek":"","iv":"","ct":""}`,
		"unknown alg":     `{"v":1,"alg":"RSA1_5+A128CBC","ek":"","iv":"","ct":""}`,
	} {
		if _, err := OpenSealedEnv(sealed, "task_123", key); err == nil {
			t.Errorf("%s: opened", name)
		}
	}

	for _, bad := range []string{"lower", "1LEADING_DIGIT", "WITH-DASH", "WITH SPACE", "A=B", ""} {
		sealed, _ := SealEnv(map[string]string{bad: "x"}, "task_123", &key.PublicKey)
		if _, err := OpenSealedEnv(sealed, "task_123", key); err == nil {
			t.Errorf("variable name %q accepted", bad)
		}
	}
}

func TestSealedEnvRefusesAValueWithANulByte(t *testing.T) {
	key := testKey(t)
	sealed, _ := SealEnv(map[string]string{"A": "x\x00y"}, "task_123", &key.PublicKey)

	if _, err := OpenSealedEnv(sealed, "task_123", key); err == nil {
		t.Fatal("a value with a NUL byte cannot be passed through the environment")
	}
}

func TestSealedEnvAsEnviron(t *testing.T) {
	got := SealedEnvAsEnviron(map[string]string{"B": "2", "A": "1=1"})
	if strings.Join(got, ",") != "A=1=1,B=2" {
		t.Fatalf("got %v", got)
	}
}
