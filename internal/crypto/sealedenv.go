package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// A task's sealed environment (selfhost #3168): secrets such as a TLS private key that the
// control plane must not write into the task's command. The control plane seals a JSON object
// of environment variables to this agent's RSA public key: a fresh AES-256-GCM key encrypts the
// object, and RSA-OAEP (SHA-256, the same parameters as credential passwords) wraps that key.
// The task ID is the additional authenticated data, so an envelope opens only for the task it
// was sealed for. Only the agent's private key can open it.
const (
	SealedEnvVersion = 1
	SealedEnvAlg     = "RSA-OAEP-256+A256GCM"
)

type sealedEnvelope struct {
	Version int    `json:"v"`
	Alg     string `json:"alg"`
	Key     string `json:"ek"`
	IV      string `json:"iv"`
	Data    string `json:"ct"`
}

// Only the control plane's own names (SELFHOST_SEALED_*): an envelope can never set PATH,
// LD_PRELOAD, BASH_ENV or anything else the script or its tools read.
var sealedEnvName = regexp.MustCompile(`^SELFHOST_SEALED_[A-Z0-9_]+$`)

// OpenSealedEnv opens a task's sealed environment with the agent's private key.
func OpenSealedEnv(sealed string, taskID string, privateKey *rsa.PrivateKey) (map[string]string, error) {
	var envelope sealedEnvelope
	if err := json.Unmarshal([]byte(sealed), &envelope); err != nil {
		return nil, fmt.Errorf("sealed env is not an envelope: %w", err)
	}
	if envelope.Version != SealedEnvVersion || envelope.Alg != SealedEnvAlg {
		return nil, fmt.Errorf("unsupported sealed env v%d %q", envelope.Version, envelope.Alg)
	}

	wrapped, err := base64.StdEncoding.DecodeString(envelope.Key)
	if err != nil {
		return nil, fmt.Errorf("sealed env key: %w", err)
	}
	iv, err := base64.StdEncoding.DecodeString(envelope.IV)
	if err != nil {
		return nil, fmt.Errorf("sealed env iv: %w", err)
	}
	data, err := base64.StdEncoding.DecodeString(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("sealed env data: %w", err)
	}

	key, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, wrapped, nil)
	if err != nil {
		return nil, fmt.Errorf("sealed env key does not open with this agent's key: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(iv) != gcm.NonceSize() {
		return nil, fmt.Errorf("sealed env iv is %d bytes, want %d", len(iv), gcm.NonceSize())
	}
	plaintext, err := gcm.Open(nil, iv, data, []byte(taskID))
	if err != nil {
		return nil, fmt.Errorf("sealed env does not open for task %s: %w", taskID, err)
	}

	var env map[string]string
	if err := json.Unmarshal(plaintext, &env); err != nil {
		return nil, fmt.Errorf("sealed env is not an object of strings: %w", err)
	}
	for name, value := range env {
		if !sealedEnvName.MatchString(name) {
			return nil, fmt.Errorf("sealed env variable name %q is not allowed", name)
		}
		if strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("sealed env variable %s contains a NUL byte", name)
		}
	}
	return env, nil
}

// SealEnv seals env for taskID to an agent's public key, as the control plane does.
func SealEnv(env map[string]string, taskID string, publicKey *rsa.PublicKey) (string, error) {
	plaintext, err := json.Marshal(env)
	if err != nil {
		return "", err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	iv := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	wrapped, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, publicKey, key, nil)
	if err != nil {
		return "", err
	}

	sealed, err := json.Marshal(sealedEnvelope{
		Version: SealedEnvVersion,
		Alg:     SealedEnvAlg,
		Key:     base64.StdEncoding.EncodeToString(wrapped),
		IV:      base64.StdEncoding.EncodeToString(iv),
		Data:    base64.StdEncoding.EncodeToString(gcm.Seal(nil, iv, plaintext, []byte(taskID))),
	})
	return string(sealed), err
}

// SealedEnvAsEnviron renders an opened environment as NAME=value entries, sorted by name.
func SealedEnvAsEnviron(env map[string]string) []string {
	entries := make([]string, 0, len(env))
	for name, value := range env {
		entries = append(entries, name+"="+value)
	}
	sort.Strings(entries)
	return entries
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("sealed env key: %w", err)
	}
	return cipher.NewGCM(block)
}
