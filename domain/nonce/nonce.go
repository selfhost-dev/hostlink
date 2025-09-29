package nonce

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"
)

type Nonce struct {
	Value     string
	CreatedAt time.Time
}

func NewNonce() *Nonce {
	bytes := make([]byte, 8)
	_, err := rand.Read(bytes)
	if err != nil {
		panic(err)
	}

	return &Nonce{
		Value:     base64.URLEncoding.EncodeToString(bytes),
		CreatedAt: time.Now(),
	}
}

func (n *Nonce) Validate() error {
	if n.Value == "" {
		return errors.New("nonce value cannot be empty")
	}

	// Check if it's valid base64
	_, err := base64.URLEncoding.DecodeString(n.Value)
	if err != nil {
		return errors.New("nonce value is not valid base64")
	}

	return nil
}

func (n *Nonce) IsExpired() bool {
	expiryDuration := 5 * time.Minute
	return time.Since(n.CreatedAt) >= expiryDuration
}