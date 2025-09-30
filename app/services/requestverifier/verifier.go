package requestverifier

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

type RequestVerifier struct {
	publicKey *rsa.PublicKey
}

func New(publicKey *rsa.PublicKey) *RequestVerifier {
	return &RequestVerifier{
		publicKey: publicKey,
	}
}

func (v *RequestVerifier) VerifyResponse(resp *http.Response) error {
	serverID := resp.Header.Get("X-Server-ID")
	if serverID == "" {
		return fmt.Errorf("missing X-Server-ID header")
	}

	timestampStr := resp.Header.Get("X-Timestamp")
	if timestampStr == "" {
		return fmt.Errorf("missing X-Timestamp header")
	}

	nonceValue := resp.Header.Get("X-Nonce")
	if nonceValue == "" {
		return fmt.Errorf("missing X-Nonce header")
	}

	signature := resp.Header.Get("X-Signature")
	if signature == "" {
		return fmt.Errorf("missing X-Signature header")
	}

	timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid timestamp format: %w", err)
	}

	if err := v.validateTimestamp(timestamp); err != nil {
		return err
	}

	if err := v.verifySignature(serverID, timestampStr, nonceValue, signature); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

func (v *RequestVerifier) validateTimestamp(timestamp int64) error {
	now := time.Now().Unix()
	diff := now - timestamp

	if diff > 300 {
		return fmt.Errorf("timestamp too old: %d seconds", diff)
	}

	if diff < -300 {
		return fmt.Errorf("timestamp too far in future: %d seconds", -diff)
	}

	return nil
}

func (v *RequestVerifier) verifySignature(serverID, timestamp, nonce, signatureBase64 string) error {
	message := fmt.Sprintf("%s|%s|%s", serverID, timestamp, nonce)
	hashed := sha256.Sum256([]byte(message))

	signatureBytes, err := base64.StdEncoding.DecodeString(signatureBase64)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	err = rsa.VerifyPSS(v.publicKey, crypto.SHA256, hashed[:], signatureBytes, nil)
	if err != nil {
		return fmt.Errorf("invalid signature: %w", err)
	}

	return nil
}