// Package e2e seals a run's data frames between the agent and the browser tab, so the relay
// forwards ciphertext. The browser's P-256 key comes in the ticket (signed by the backend); the
// agent answers `accepted` with its own P-256 key, signed with the agent's RSA key; both derive
// one AES-256-GCM key with HKDF-SHA256. A data frame's nonce is its sequence number and its
// additional data is the frame's prefix. Contract: the backend's docs/sql-stream-protocol.md,
// section 4. Browser here is for tests and the developer probe; the console's is WebCrypto.
package e2e

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	acceptedContext = "selfhost sql-stream v1 accepted\n"
	keyInfo         = "selfhost sql-stream v1 agent-to-browser"
	// PSSSaltLength is fixed: WebCrypto's RSA-PSS verify needs the salt's length.
	PSSSaltLength = 32
)

var b64 = base64.RawURLEncoding

// Accepted is the body of an `accepted` frame.
type Accepted struct {
	APK string `json:"apk"`
	Sig string `json:"sig"`
}

// AcceptedMessage is what the agent signs: the run, its key and the browser's, as b64u text.
func AcceptedMessage(run, apk, bpk string) []byte {
	return []byte(acceptedContext + run + "\n" + apk + "\n" + bpk)
}

// ParsePublicKey reads a P-256 public key, an uncompressed point, b64u.
func ParsePublicKey(encoded string) (*ecdh.PublicKey, error) {
	raw, err := b64.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("key is not base64url")
	}
	if len(raw) != 65 || raw[0] != 4 {
		return nil, errors.New("key is not an uncompressed P-256 point")
	}
	return ecdh.P256().NewPublicKey(raw)
}

// DeriveKey is HKDF-SHA256 over the ECDH secret, salted with the run pid.
func DeriveKey(secret []byte, run string) ([]byte, error) {
	return hkdf.Key(sha256.New, secret, []byte(run), keyInfo, 32)
}

// Nonce is four zero bytes, then seq as eight bytes, big-endian.
func Nonce(seq uint64) []byte {
	nonce := make([]byte, 12)
	binary.BigEndian.PutUint64(nonce[4:], seq)
	return nonce
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// Sealer seals one run's data frames on the agent.
type Sealer struct {
	aead cipher.AEAD
}

// Accept answers a ticket's browser key: it makes the agent's key pair for the run, signs it
// and returns the `accepted` body and the run's sealer.
func Accept(run, bpk string, agentKey *rsa.PrivateKey) ([]byte, *Sealer, error) {
	return accept(rand.Reader, run, bpk, agentKey)
}

func accept(random io.Reader, run, bpk string, agentKey *rsa.PrivateKey) ([]byte, *Sealer, error) {
	browserKey, err := ParsePublicKey(bpk)
	if err != nil {
		return nil, nil, fmt.Errorf("browser key: %w", err)
	}
	priv, err := ecdh.P256().GenerateKey(random)
	if err != nil {
		return nil, nil, err
	}
	return acceptWith(random, priv, run, bpk, browserKey, agentKey)
}

func acceptWith(random io.Reader, priv *ecdh.PrivateKey, run, bpk string, browserKey *ecdh.PublicKey, agentKey *rsa.PrivateKey) ([]byte, *Sealer, error) {
	apk := b64.EncodeToString(priv.PublicKey().Bytes())
	digest := sha256.Sum256(AcceptedMessage(run, apk, bpk))
	sig, err := rsa.SignPSS(random, agentKey, crypto.SHA256, digest[:], &rsa.PSSOptions{SaltLength: PSSSaltLength, Hash: crypto.SHA256})
	if err != nil {
		return nil, nil, fmt.Errorf("signing the key: %w", err)
	}
	secret, err := priv.ECDH(browserKey)
	if err != nil {
		return nil, nil, err
	}
	key, err := DeriveKey(secret, run)
	if err != nil {
		return nil, nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, nil, err
	}
	body, err := json.Marshal(Accepted{APK: apk, Sig: b64.EncodeToString(sig)})
	if err != nil {
		return nil, nil, err
	}
	return body, &Sealer{aead: aead}, nil
}

// AcceptWith is Accept with the agent's run key given: for the test vectors.
func AcceptWith(priv *ecdh.PrivateKey, run, bpk string, agentKey *rsa.PrivateKey) ([]byte, *Sealer, error) {
	browserKey, err := ParsePublicKey(bpk)
	if err != nil {
		return nil, nil, fmt.Errorf("browser key: %w", err)
	}
	return acceptWith(rand.Reader, priv, run, bpk, browserKey, agentKey)
}

// NewSealerForKey seals with a key already derived: for the test vectors.
func NewSealerForKey(key []byte) (*Sealer, error) {
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal seals a data frame's body: prefix is the frame's prefix (wire.Prefix), its additional data.
func (s *Sealer) Seal(prefix []byte, seq uint64, plaintext []byte) []byte {
	return s.aead.Seal(nil, Nonce(seq), plaintext, prefix)
}

// Opener opens one run's data frames: the browser's side.
type Opener struct {
	aead cipher.AEAD
}

// BrowserKey makes the browser's key pair for a run and returns it with its public key, b64u.
func BrowserKey() (*ecdh.PrivateKey, string, error) {
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", err
	}
	return priv, b64.EncodeToString(priv.PublicKey().Bytes()), nil
}

// ErrNotTheAgent is an `accepted` whose key the agent did not sign.
var ErrNotTheAgent = errors.New("the agent's key is not signed by the agent")

// Open checks an `accepted` body against the agent's RSA public key and derives the run's key.
func Open(run string, browserPriv *ecdh.PrivateKey, bpk string, acceptedBody []byte, agentPub *rsa.PublicKey) (*Opener, error) {
	var a Accepted
	if err := json.Unmarshal(acceptedBody, &a); err != nil {
		return nil, fmt.Errorf("accepted is not JSON: %w", err)
	}
	sig, err := b64.DecodeString(a.Sig)
	if err != nil {
		return nil, ErrNotTheAgent
	}
	digest := sha256.Sum256(AcceptedMessage(run, a.APK, bpk))
	if err := rsa.VerifyPSS(agentPub, crypto.SHA256, digest[:], sig, &rsa.PSSOptions{SaltLength: PSSSaltLength, Hash: crypto.SHA256}); err != nil {
		return nil, ErrNotTheAgent
	}
	agentKey, err := ParsePublicKey(a.APK)
	if err != nil {
		return nil, fmt.Errorf("agent key: %w", err)
	}
	secret, err := browserPriv.ECDH(agentKey)
	if err != nil {
		return nil, err
	}
	key, err := DeriveKey(secret, run)
	if err != nil {
		return nil, err
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &Opener{aead: aead}, nil
}

// Open opens a data frame's body.
func (o *Opener) Open(prefix []byte, seq uint64, sealed []byte) ([]byte, error) {
	return o.aead.Open(nil, Nonce(seq), sealed, prefix)
}
