package reqauth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

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

	message := fmt.Sprintf("%s|%d|%s", agentID, timestamp, nonce)
	hashed := sha256.Sum256([]byte(message))

	err = rsa.VerifyPSS(a.publicKey, crypto.SHA256, hashed[:], signature, nil)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}
