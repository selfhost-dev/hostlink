// Package crypto provides cryptographic utilities
package crypto

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// Service defines cryptographic operations for RSA key management and encryption
type Service interface {
	// Key Generation and Management
	GenerateKeypair(bits int) (*rsa.PrivateKey, error)
	LoadOrGenerateKeypair(keyPath string, bits int) (*rsa.PrivateKey, error)

	// Private Key Operations
	SavePrivateKey(privateKey *rsa.PrivateKey, keyPath string) error
	LoadPrivateKey(keyPath string) (*rsa.PrivateKey, error)

	// Public Key Operations
	LoadPublicKey(keyPath string) (*rsa.PublicKey, error)
	GetPublicKeyBase64(privateKey *rsa.PrivateKey) (string, error)
	GetPublicKeyPEM(privateKey *rsa.PrivateKey) (string, error)

	// Public Key Parsing
	ParsePublicKeyFromBase64(base64String string) (*rsa.PublicKey, error)
	ParsePublicKeyFromPEM(pemString string) (*rsa.PublicKey, error)

	// Encryption/Decryption
	EncryptWithPublicKey(msg string, pub *rsa.PublicKey) (string, error)
	DecryptWithPrivateKey(ciphertextBase64 string, privateKey *rsa.PrivateKey) (string, error)
}

// DefaultCryptoService implements CryptoService using the existing functions
type DefaultCryptoService struct{}

// NewService creates a new instance of the default crypto service
func NewService() Service {
	return &DefaultCryptoService{}
}

func (s *DefaultCryptoService) GenerateKeypair(bits int) (*rsa.PrivateKey, error) {
	return GenerateRSAKeypair(bits)
}

func (s *DefaultCryptoService) LoadOrGenerateKeypair(keyPath string, bits int) (*rsa.PrivateKey, error) {
	return LoadOrGenerateKeypair(keyPath, bits)
}

func (s *DefaultCryptoService) SavePrivateKey(privateKey *rsa.PrivateKey, keyPath string) error {
	return SavePrivateKey(privateKey, keyPath)
}

func (s *DefaultCryptoService) LoadPrivateKey(keyPath string) (*rsa.PrivateKey, error) {
	return LoadPrivateKey(keyPath)
}

func (s *DefaultCryptoService) LoadPublicKey(keyPath string) (*rsa.PublicKey, error) {
	return LoadPublicKey(keyPath)
}

func (s *DefaultCryptoService) GetPublicKeyBase64(privateKey *rsa.PrivateKey) (string, error) {
	return GetPublicKeyBase64(privateKey)
}

func (s *DefaultCryptoService) GetPublicKeyPEM(privateKey *rsa.PrivateKey) (string, error) {
	return GetPublicKeyPEM(privateKey)
}

func (s *DefaultCryptoService) ParsePublicKeyFromBase64(base64String string) (*rsa.PublicKey, error) {
	return ParsePublicKeyFromBase64(base64String)
}

func (s *DefaultCryptoService) ParsePublicKeyFromPEM(pemString string) (*rsa.PublicKey, error) {
	return ParsePublicKeyFromPEM(pemString)
}

func (s *DefaultCryptoService) EncryptWithPublicKey(msg string, pub *rsa.PublicKey) (string, error) {
	return EncryptWithPublicKey(msg, pub)
}

func (s *DefaultCryptoService) DecryptWithPrivateKey(ciphertextBase64 string, privateKey *rsa.PrivateKey) (string, error) {
	return DecryptWithPrivateKey(ciphertextBase64, privateKey)
}

// GenerateRSAKeypair generates a new RSA keypair
func GenerateRSAKeypair(bits int) (*rsa.PrivateKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		return nil, fmt.Errorf("failed to generate RSA key: %w", err)
	}
	return privateKey, nil
}

// SavePrivateKey saves an RSA private key to a file in PEM format
func SavePrivateKey(privateKey *rsa.PrivateKey, keyPath string) error {
	// Create the file
	file, err := os.OpenFile(keyPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to create private key file: %w", err)
	}
	defer file.Close()

	// Ensure correct permissions even if file already existed
	if err := file.Chmod(0600); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	// Marshal the private key
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)

	// Create PEM block
	privateKeyPEM := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}

	// Write to file
	if err := pem.Encode(file, privateKeyPEM); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	return nil
}

// LoadPrivateKey loads an RSA private key from a PEM file
func LoadPrivateKey(keyPath string) (*rsa.PrivateKey, error) {
	// Read the file
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key file: %w", err)
	}

	// Decode PEM
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}

	// Parse the private key
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8 format as fallback
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}

		var ok bool
		privateKey, ok = key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("key is not an RSA private key")
		}
	}

	return privateKey, nil
}

// GetPublicKeyBase64 returns the public key in Base64 format
func GetPublicKeyBase64(privateKey *rsa.PrivateKey) (string, error) {
	// Get the public key
	publicKey := &privateKey.PublicKey

	// Marshal the public key
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("failed to marshal public key: %w", err)
	}

	// Encode to base64
	publicKeyBase64 := base64.StdEncoding.EncodeToString(publicKeyBytes)

	return publicKeyBase64, nil
}

// GetPublicKeyPEM returns the public key in PEM format
func GetPublicKeyPEM(privateKey *rsa.PrivateKey) (string, error) {
	// Get the public key
	publicKey := &privateKey.PublicKey

	// Marshal the public key
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return "", fmt.Errorf("failed to marshal public key: %w", err)
	}

	// Create PEM block
	publicKeyPEM := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}

	// Encode to string
	pemBytes := pem.EncodeToMemory(publicKeyPEM)
	return string(pemBytes), nil
}

// LoadPublicKey loads an RSA public key from a PEM file
func LoadPublicKey(keyPath string) (*rsa.PublicKey, error) {
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read public key file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to parse PEM block")
	}

	publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPublicKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not an RSA public key")
	}

	return rsaPublicKey, nil
}

// ParsePublicKey takes a decoder function that converts input string to bytes
func ParsePublicKey(input string, decoder func(string) ([]byte, error)) (*rsa.PublicKey, error) {
	keyBytes, err := decoder(input)
	if err != nil {
		return nil, fmt.Errorf("failed to decode input: %w", err)
	}

	publicKey, err := x509.ParsePKIXPublicKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPublicKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("key is not an RSA public key")
	}

	return rsaPublicKey, nil
}

// ParsePublicKeyFromBase64 parses an RSA public key from a Base64 string
func ParsePublicKeyFromBase64(base64String string) (*rsa.PublicKey, error) {
	return ParsePublicKey(base64String, func(s string) ([]byte, error) {
		return base64.StdEncoding.DecodeString(s)
	})
}

// ParsePublicKeyFromPEM parses an RSA public key from a PEM string
func ParsePublicKeyFromPEM(pemString string) (*rsa.PublicKey, error) {
	return ParsePublicKey(pemString, func(s string) ([]byte, error) {
		block, _ := pem.Decode([]byte(s))
		if block == nil {
			return nil, fmt.Errorf("failed to parse PEM block")
		}
		return block.Bytes, nil
	})
}

// LoadOrGenerateKeypair loads an existing keypair or generates a new one
func LoadOrGenerateKeypair(keyPath string, bits int) (*rsa.PrivateKey, error) {
	// Try to load existing key
	if _, err := os.Stat(keyPath); err == nil {
		privateKey, err := LoadPrivateKey(keyPath)
		if err == nil {
			return privateKey, nil
		}
		// If loading failed, generate new one
	}

	// Generate new keypair
	privateKey, err := GenerateRSAKeypair(bits)
	if err != nil {
		return nil, err
	}

	// Ensure directory exists
	dir := filepath.Dir(keyPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create directory: %w", err)
	}

	// Save the private key
	if err := SavePrivateKey(privateKey, keyPath); err != nil {
		return nil, err
	}

	return privateKey, nil
}

// DecryptWithPrivateKey decrypts a base64 encoded ciphertext with a private key
func DecryptWithPrivateKey(ciphertextBase64 string, privateKey *rsa.PrivateKey) (string, error) {
	ciphertext, err := base64.StdEncoding.DecodeString(ciphertextBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	plaintext, err := rsa.DecryptOAEP(sha256.New(), rand.Reader, privateKey, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt ciphertext: %w", err)
	}

	return string(plaintext), nil
}

// EncryptWithPublicKey encrypts a message with a public key
func EncryptWithPublicKey(msg string, pub *rsa.PublicKey) (string, error) {
	ciphertext, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, pub, []byte(msg), nil)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt message: %w", err)
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}
