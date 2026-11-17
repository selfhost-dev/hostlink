package reqauth

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// SignatureVersionHeader selects how the client signed the request (#4113).
// Absent means version 1, the message every deployed agent sends; version 2
// binds the signature to the request line and body so it cannot be replayed
// against another endpoint.
const SignatureVersionHeader = "X-Agent-Signature-Version"

type Authenticator struct {
	publicKey *rsa.PublicKey
}

func New(publicKey *rsa.PublicKey) *Authenticator {
	return &Authenticator{
		publicKey: publicKey,
	}
}

func (a *Authenticator) Authenticate(r *http.Request) error {
	agentID := r.Header.Get("X-Agent-ID")
	if agentID == "" {
		return fmt.Errorf("missing X-Agent-ID header")
	}

	timestampStr := r.Header.Get("X-Timestamp")
	if timestampStr == "" {
		return fmt.Errorf("missing X-Timestamp header")
	}

	nonce := r.Header.Get("X-Nonce")
	if nonce == "" {
		return fmt.Errorf("missing X-Nonce header")
	}

	signatureStr := r.Header.Get("X-Signature")
	if signatureStr == "" {
		return fmt.Errorf("missing X-Signature header")
	}

	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp: %w", err)
	}

	now := time.Now().Unix()
	diff := now - timestamp
	if diff > 300 || diff < -300 {
		return fmt.Errorf("timestamp outside valid window")
	}

	signature, err := base64.StdEncoding.DecodeString(signatureStr)
	if err != nil {
		return fmt.Errorf("invalid signature encoding: %w", err)
	}

	message, err := signatureMessage(r, agentID, timestamp, nonce)
	if err != nil {
		return err
	}
	hashed := sha256.Sum256([]byte(message))

	err = rsa.VerifyPSS(a.publicKey, crypto.SHA256, hashed[:], signature, nil)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

// signatureMessage rebuilds exactly what the client signed. Version 1 is the
// deployed message (agent|timestamp|nonce); version 2 adds the request line and
// the body digest, so a signature captured on one request cannot be moved to
// another path, method or payload.
func signatureMessage(r *http.Request, agentID string, timestamp int64, nonce string) (string, error) {
	switch strings.TrimSpace(r.Header.Get(SignatureVersionHeader)) {
	case "", "1":
		return fmt.Sprintf("%s|%d|%s", agentID, timestamp, nonce), nil

	case "2":
		digest, err := requestBodyDigest(r)
		if err != nil {
			return "", fmt.Errorf("failed to read request body: %w", err)
		}
		return fmt.Sprintf("%s|%d|%s|%s|%s|%s", agentID, timestamp, nonce, r.Method, r.URL.Path, digest), nil

	default:
		return "", fmt.Errorf("unsupported signature version %q", r.Header.Get(SignatureVersionHeader))
	}
}

// requestBodyDigest hashes the exact bytes the client signed and leaves the
// body readable for the handler (the authenticator runs before it).
func requestBodyDigest(r *http.Request) (string, error) {
	if r.Body == nil {
		return sha256Hex(nil), nil
	}

	if r.GetBody != nil {
		body, err := r.GetBody()
		if err != nil {
			return "", err
		}
		defer body.Close()
		data, err := io.ReadAll(body)
		if err != nil {
			return "", err
		}
		return sha256Hex(data), nil
	}

	data, err := io.ReadAll(r.Body)
	if err != nil {
		return "", err
	}
	r.Body = io.NopCloser(bytes.NewReader(data))
	return sha256Hex(data), nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
