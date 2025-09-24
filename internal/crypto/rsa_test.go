package crypto

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"testing"
)

func TestGenerateRSAKeypair(t *testing.T) {
	t.Run("should generate valid 2048-bit RSA keypair", func(t *testing.T) {
		key, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		if key == nil {
			t.Fatal("Generated key is nil")
		}

		if key.N.BitLen() != 2048 {
			t.Errorf("Expected 2048-bit key, got %d bits", key.N.BitLen())
		}

		if err := key.Validate(); err != nil {
			t.Errorf("Generated key is invalid: %v", err)
		}

		if key.PublicKey.E == 0 {
			t.Error("Public exponent is 0")
		}
	})

	t.Run("should generate valid 4096-bit RSA keypair", func(t *testing.T) {
		key, err := GenerateRSAKeypair(4096)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		if key == nil {
			t.Fatal("Generated key is nil")
		}

		if key.N.BitLen() != 4096 {
			t.Errorf("Expected 4096-bit key, got %d bits", key.N.BitLen())
		}

		if err := key.Validate(); err != nil {
			t.Errorf("Generated key is invalid: %v", err)
		}

		if key.PublicKey.E == 0 {
			t.Error("Public exponent is 0")
		}
	})

	t.Run("should generate unique keypairs on each call", func(t *testing.T) {
		key1, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate first RSA keypair: %v", err)
		}

		key2, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate second RSA keypair: %v", err)
		}

		if key1.N.Cmp(key2.N) == 0 {
			t.Error("Generated keys have identical modulus")
		}

		if key1.D.Cmp(key2.D) == 0 {
			t.Error("Generated keys have identical private exponent")
		}

		if len(key1.Primes) == len(key2.Primes) && len(key1.Primes) >= 2 {
			if key1.Primes[0].Cmp(key2.Primes[0]) == 0 && key1.Primes[1].Cmp(key2.Primes[1]) == 0 {
				t.Error("Generated keys have identical prime factors")
			}
		}
	})
}

func TestSavePrivateKey(t *testing.T) {
	t.Run("should save private key to file with correct permissions", func(t *testing.T) {
		key, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		tmpFile, cleanup := setupTempFile(t)
		defer cleanup()

		err = SavePrivateKey(key, tmpFile)
		if err != nil {
			t.Fatalf("Failed to save private key: %v", err)
		}

		info, err := os.Stat(tmpFile)
		if err != nil {
			t.Fatalf("Failed to stat file: %v", err)
		}

		perm := info.Mode().Perm()
		if perm != 0600 {
			t.Errorf("Expected file permissions 0600, got %04o", perm)
		}

		content, err := os.ReadFile(tmpFile)
		if err != nil {
			t.Fatalf("Failed to read file: %v", err)
		}

		if len(content) == 0 {
			t.Error("File is empty")
		}
	})

	t.Run("should save key in PEM format", func(t *testing.T) {
		key, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		tmpFile, cleanup := setupTempFile(t)
		defer cleanup()

		err = SavePrivateKey(key, tmpFile)
		if err != nil {
			t.Fatalf("Failed to save private key: %v", err)
		}

		content, err := os.ReadFile(tmpFile)
		if err != nil {
			t.Fatalf("Failed to read file: %v", err)
		}

		if !bytes.Contains(content, []byte("-----BEGIN RSA PRIVATE KEY-----")) {
			t.Error("PEM file missing BEGIN header")
		}

		if !bytes.Contains(content, []byte("-----END RSA PRIVATE KEY-----")) {
			t.Error("PEM file missing END footer")
		}

		block, _ := pem.Decode(content)
		if block == nil {
			t.Fatal("Failed to decode PEM block")
		}

		if block.Type != "RSA PRIVATE KEY" {
			t.Errorf("Expected block type 'RSA PRIVATE KEY', got '%s'", block.Type)
		}

		_, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			t.Errorf("Failed to parse private key from PEM: %v", err)
		}
	})

	t.Run("should overwrite existing file", func(t *testing.T) {
		key1, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate first RSA keypair: %v", err)
		}

		key2, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate second RSA keypair: %v", err)
		}

		tmpFile, cleanup := setupTempFile(t)
		defer cleanup()

		// Save first key
		err = SavePrivateKey(key1, tmpFile)
		if err != nil {
			t.Fatalf("Failed to save first private key: %v", err)
		}

		content1, err := os.ReadFile(tmpFile)
		if err != nil {
			t.Fatalf("Failed to read file after first save: %v", err)
		}

		// Save second key to same file
		err = SavePrivateKey(key2, tmpFile)
		if err != nil {
			t.Fatalf("Failed to save second private key: %v", err)
		}

		content2, err := os.ReadFile(tmpFile)
		if err != nil {
			t.Fatalf("Failed to read file after second save: %v", err)
		}

		if bytes.Equal(content1, content2) {
			t.Error("File content unchanged after overwriting with different key")
		}

		// Verify the loaded key is the second one
		loadedKey, err := LoadPrivateKey(tmpFile)
		if err != nil {
			t.Fatalf("Failed to load private key: %v", err)
		}

		if loadedKey.N.Cmp(key2.N) != 0 {
			t.Error("Loaded key does not match the second saved key")
		}

		if loadedKey.N.Cmp(key1.N) == 0 {
			t.Error("Loaded key incorrectly matches the first saved key")
		}
	})

	t.Run("should return error for invalid path", func(t *testing.T) {
		key, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		// Try to save to an invalid path (directory that doesn't exist)
		invalidPath := "/nonexistent/directory/key.pem"
		err = SavePrivateKey(key, invalidPath)
		if err == nil {
			t.Error("Expected error when saving to invalid path, but got nil")
		}

		// Try to save to a path that is a directory
		tempDir := t.TempDir()
		err = SavePrivateKey(key, tempDir)
		if err == nil {
			t.Error("Expected error when saving to directory path, but got nil")
		}
	})
}

func TestLoadPrivateKey(t *testing.T) {
	t.Run("should load valid private key from PEM file", func(t *testing.T) {
		// Generate and save a key first
		originalKey, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		tmpFile, cleanup := setupTempFile(t)
		defer cleanup()

		err = SavePrivateKey(originalKey, tmpFile)
		if err != nil {
			t.Fatalf("Failed to save private key: %v", err)
		}

		// Load the key back
		loadedKey, err := LoadPrivateKey(tmpFile)
		if err != nil {
			t.Fatalf("Failed to load private key: %v", err)
		}

		if loadedKey == nil {
			t.Fatal("Loaded key is nil")
		}

		// Verify the loaded key matches the original
		if loadedKey.N.Cmp(originalKey.N) != 0 {
			t.Error("Loaded key modulus doesn't match original")
		}

		if loadedKey.D.Cmp(originalKey.D) != 0 {
			t.Error("Loaded key private exponent doesn't match original")
		}

		if loadedKey.PublicKey.E != originalKey.PublicKey.E {
			t.Error("Loaded key public exponent doesn't match original")
		}

		// Verify the loaded key is valid
		if err := loadedKey.Validate(); err != nil {
			t.Errorf("Loaded key is invalid: %v", err)
		}
	})

	t.Run("should return error for non-existent file", func(t *testing.T) {
		nonExistentFile := "/tmp/non_existent_key_file.pem"

		_, err := LoadPrivateKey(nonExistentFile)
		if err == nil {
			t.Error("Expected error when loading non-existent file, but got nil")
		}

		if !os.IsNotExist(err) {
			// Check if the error contains file not found message
			if !bytes.Contains([]byte(err.Error()), []byte("no such file")) &&
			   !bytes.Contains([]byte(err.Error()), []byte("cannot find")) {
				t.Logf("Error might not be a file not found error: %v", err)
			}
		}
	})

	t.Run("should return error for invalid PEM format", func(t *testing.T) {
		tmpFile, cleanup := setupTempFile(t)
		defer cleanup()

		// Write invalid PEM content
		invalidContent := []byte("This is not a valid PEM file content")
		err := os.WriteFile(tmpFile, invalidContent, 0600)
		if err != nil {
			t.Fatalf("Failed to write test file: %v", err)
		}

		_, err = LoadPrivateKey(tmpFile)
		if err == nil {
			t.Error("Expected error when loading invalid PEM format, but got nil")
		}

		if !bytes.Contains([]byte(err.Error()), []byte("failed to parse PEM block")) {
			t.Errorf("Expected PEM parse error, got: %v", err)
		}
	})

	t.Run("should support PKCS1 format", func(t *testing.T) {
		// Generate a key and save it in PKCS1 format (which is what SavePrivateKey does)
		originalKey, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		tmpFile, cleanup := setupTempFile(t)
		defer cleanup()

		// Manually create PKCS1 PEM content
		privateKeyBytes := x509.MarshalPKCS1PrivateKey(originalKey)
		privateKeyPEM := &pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: privateKeyBytes,
		}

		file, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		if err := pem.Encode(file, privateKeyPEM); err != nil {
			file.Close()
			t.Fatalf("Failed to write PEM: %v", err)
		}
		file.Close()

		// Load the PKCS1 format key
		loadedKey, err := LoadPrivateKey(tmpFile)
		if err != nil {
			t.Fatalf("Failed to load PKCS1 private key: %v", err)
		}

		if loadedKey.N.Cmp(originalKey.N) != 0 {
			t.Error("Loaded PKCS1 key modulus doesn't match original")
		}

		if loadedKey.D.Cmp(originalKey.D) != 0 {
			t.Error("Loaded PKCS1 key private exponent doesn't match original")
		}
	})

	t.Run("should support PKCS8 format", func(t *testing.T) {
		// Generate a key and save it in PKCS8 format
		originalKey, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		tmpFile, cleanup := setupTempFile(t)
		defer cleanup()

		// Manually create PKCS8 PEM content
		privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(originalKey)
		if err != nil {
			t.Fatalf("Failed to marshal PKCS8: %v", err)
		}

		privateKeyPEM := &pem.Block{
			Type:  "PRIVATE KEY",
			Bytes: privateKeyBytes,
		}

		file, err := os.OpenFile(tmpFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		if err := pem.Encode(file, privateKeyPEM); err != nil {
			file.Close()
			t.Fatalf("Failed to write PEM: %v", err)
		}
		file.Close()

		// Load the PKCS8 format key
		loadedKey, err := LoadPrivateKey(tmpFile)
		if err != nil {
			t.Fatalf("Failed to load PKCS8 private key: %v", err)
		}

		if loadedKey.N.Cmp(originalKey.N) != 0 {
			t.Error("Loaded PKCS8 key modulus doesn't match original")
		}

		if loadedKey.D.Cmp(originalKey.D) != 0 {
			t.Error("Loaded PKCS8 key private exponent doesn't match original")
		}
	})
}

func TestGetPublicKeyBase64(t *testing.T) {
	t.Run("should extract public key in Base64 format", func(t *testing.T) {
		key, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		base64Key, err := GetPublicKeyBase64(key)
		if err != nil {
			t.Fatalf("Failed to get public key in Base64: %v", err)
		}

		if base64Key == "" {
			t.Error("Base64 public key is empty")
		}

		// Decode Base64 to verify it's valid
		decodedBytes, err := base64.StdEncoding.DecodeString(base64Key)
		if err != nil {
			t.Errorf("Failed to decode Base64: %v", err)
		}

		// Parse the decoded bytes as a public key
		parsedKey, err := x509.ParsePKIXPublicKey(decodedBytes)
		if err != nil {
			t.Errorf("Failed to parse decoded public key: %v", err)
		}

		rsaPubKey, ok := parsedKey.(*rsa.PublicKey)
		if !ok {
			t.Error("Parsed key is not an RSA public key")
		}

		// Verify it matches the original public key
		if rsaPubKey.N.Cmp(key.N) != 0 {
			t.Error("Decoded public key modulus doesn't match original")
		}

		if rsaPubKey.E != key.PublicKey.E {
			t.Error("Decoded public key exponent doesn't match original")
		}
	})

	t.Run("should return valid Base64 string", func(t *testing.T) {
		key, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		base64Key, err := GetPublicKeyBase64(key)
		if err != nil {
			t.Fatalf("Failed to get public key in Base64: %v", err)
		}

		// Verify it can be decoded without error (this validates Base64 format)
		decodedBytes, err := base64.StdEncoding.DecodeString(base64Key)
		if err != nil {
			t.Errorf("Invalid Base64 string: %v", err)
		}

		// Verify the decoded content is not empty
		if len(decodedBytes) == 0 {
			t.Error("Decoded Base64 resulted in empty bytes")
		}

		// Verify length is reasonable for a 2048-bit RSA public key
		// A 2048-bit RSA public key in DER format is typically around 294 bytes
		// In Base64, this would be around 392 characters
		if len(base64Key) < 300 || len(base64Key) > 500 {
			t.Errorf("Unexpected Base64 string length: %d", len(base64Key))
		}
	})

	t.Run("should return consistent output for same private key", func(t *testing.T) {
		key, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		// Get Base64 public key multiple times
		base64Key1, err := GetPublicKeyBase64(key)
		if err != nil {
			t.Fatalf("Failed to get public key in Base64 (1st call): %v", err)
		}

		base64Key2, err := GetPublicKeyBase64(key)
		if err != nil {
			t.Fatalf("Failed to get public key in Base64 (2nd call): %v", err)
		}

		base64Key3, err := GetPublicKeyBase64(key)
		if err != nil {
			t.Fatalf("Failed to get public key in Base64 (3rd call): %v", err)
		}

		// All calls should return identical output
		if base64Key1 != base64Key2 {
			t.Error("First and second calls returned different Base64 strings")
		}

		if base64Key2 != base64Key3 {
			t.Error("Second and third calls returned different Base64 strings")
		}

		if base64Key1 != base64Key3 {
			t.Error("First and third calls returned different Base64 strings")
		}
	})
}

func TestGetPublicKeyPEM(t *testing.T) {
	t.Run("should extract public key in PEM format", func(t *testing.T) {
		key, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		pemKey, err := GetPublicKeyPEM(key)
		if err != nil {
			t.Fatalf("Failed to get public key in PEM: %v", err)
		}

		if pemKey == "" {
			t.Error("PEM public key is empty")
		}

		// Decode the PEM block
		block, _ := pem.Decode([]byte(pemKey))
		if block == nil {
			t.Fatal("Failed to decode PEM block")
		}

		if block.Type != "PUBLIC KEY" {
			t.Errorf("Expected block type 'PUBLIC KEY', got '%s'", block.Type)
		}

		// Parse the public key from the PEM block
		parsedKey, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			t.Fatalf("Failed to parse public key from PEM: %v", err)
		}

		rsaPubKey, ok := parsedKey.(*rsa.PublicKey)
		if !ok {
			t.Fatal("Parsed key is not an RSA public key")
		}

		// Verify it matches the original public key
		if rsaPubKey.N.Cmp(key.N) != 0 {
			t.Error("PEM public key modulus doesn't match original")
		}

		if rsaPubKey.E != key.PublicKey.E {
			t.Error("PEM public key exponent doesn't match original")
		}
	})

	t.Run("should include correct PEM headers", func(t *testing.T) {
		key, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		pemKey, err := GetPublicKeyPEM(key)
		if err != nil {
			t.Fatalf("Failed to get public key in PEM: %v", err)
		}

		// Check for BEGIN header
		if !bytes.Contains([]byte(pemKey), []byte("-----BEGIN PUBLIC KEY-----")) {
			t.Error("PEM missing BEGIN PUBLIC KEY header")
		}

		// Check for END footer
		if !bytes.Contains([]byte(pemKey), []byte("-----END PUBLIC KEY-----")) {
			t.Error("PEM missing END PUBLIC KEY footer")
		}

		// Check that headers are on separate lines
		lines := bytes.Split([]byte(pemKey), []byte("\n"))
		if len(lines) < 3 {
			t.Error("PEM should have at least 3 lines (header, content, footer)")
		}

		// First line should be the BEGIN header
		if !bytes.Equal(bytes.TrimSpace(lines[0]), []byte("-----BEGIN PUBLIC KEY-----")) {
			t.Error("First line should be the BEGIN header")
		}

		// Last non-empty line should be the END footer
		for i := len(lines) - 1; i >= 0; i-- {
			if len(bytes.TrimSpace(lines[i])) > 0 {
				if !bytes.Equal(bytes.TrimSpace(lines[i]), []byte("-----END PUBLIC KEY-----")) {
					t.Error("Last non-empty line should be the END footer")
				}
				break
			}
		}
	})
}

func TestLoadOrGenerateKeypair(t *testing.T) {
	t.Run("should generate new keypair when file doesn't exist", func(t *testing.T) {
		tempDir := t.TempDir()
		keyPath := tempDir + "/new_key.pem"

		// Ensure file doesn't exist
		if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
			t.Fatal("Test file already exists")
		}

		// Load or generate (should generate)
		key, err := LoadOrGenerateKeypair(keyPath, 2048)
		if err != nil {
			t.Fatalf("Failed to load or generate keypair: %v", err)
		}

		if key == nil {
			t.Fatal("Generated key is nil")
		}

		// Verify key is valid
		if err := key.Validate(); err != nil {
			t.Errorf("Generated key is invalid: %v", err)
		}

		// Verify file was created
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			t.Error("Key file was not created")
		}

		// Verify saved file can be loaded
		loadedKey, err := LoadPrivateKey(keyPath)
		if err != nil {
			t.Fatalf("Failed to load saved key: %v", err)
		}

		if loadedKey.N.Cmp(key.N) != 0 {
			t.Error("Loaded key doesn't match generated key")
		}
	})

	t.Run("should load existing keypair when file exists", func(t *testing.T) {
		tempDir := t.TempDir()
		keyPath := tempDir + "/existing_key.pem"

		// First generate and save a key
		originalKey, err := GenerateRSAKeypair(2048)
		if err != nil {
			t.Fatalf("Failed to generate RSA keypair: %v", err)
		}

		err = SavePrivateKey(originalKey, keyPath)
		if err != nil {
			t.Fatalf("Failed to save private key: %v", err)
		}

		// Now use LoadOrGenerateKeypair (should load existing)
		loadedKey, err := LoadOrGenerateKeypair(keyPath, 2048)
		if err != nil {
			t.Fatalf("Failed to load or generate keypair: %v", err)
		}

		if loadedKey == nil {
			t.Fatal("Loaded key is nil")
		}

		// Verify loaded key matches the original
		if loadedKey.N.Cmp(originalKey.N) != 0 {
			t.Error("Loaded key modulus doesn't match original")
		}

		if loadedKey.D.Cmp(originalKey.D) != 0 {
			t.Error("Loaded key private exponent doesn't match original")
		}

		// Verify file was not regenerated (modification time should be the same)
		info1, err := os.Stat(keyPath)
		if err != nil {
			t.Fatalf("Failed to stat file: %v", err)
		}

		// Call LoadOrGenerateKeypair again
		loadedKey2, err := LoadOrGenerateKeypair(keyPath, 2048)
		if err != nil {
			t.Fatalf("Failed to load or generate keypair (2nd call): %v", err)
		}

		info2, err := os.Stat(keyPath)
		if err != nil {
			t.Fatalf("Failed to stat file after second call: %v", err)
		}

		// Modification time should be the same
		if info1.ModTime() != info2.ModTime() {
			t.Error("File was modified when it should have been loaded")
		}

		// Keys should still match
		if loadedKey2.N.Cmp(originalKey.N) != 0 {
			t.Error("Second loaded key doesn't match original")
		}
	})

	t.Run("should create directory if it doesn't exist", func(t *testing.T) {
		tempDir := t.TempDir()
		keyPath := tempDir + "/nested/dir/path/key.pem"

		// Ensure directory doesn't exist
		dir := tempDir + "/nested"
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatal("Test directory already exists")
		}

		// Load or generate (should create directories and generate)
		key, err := LoadOrGenerateKeypair(keyPath, 2048)
		if err != nil {
			t.Fatalf("Failed to load or generate keypair: %v", err)
		}

		if key == nil {
			t.Fatal("Generated key is nil")
		}

		// Verify directories were created
		if _, err := os.Stat(tempDir + "/nested/dir/path"); os.IsNotExist(err) {
			t.Error("Directory structure was not created")
		}

		// Verify file was created
		if _, err := os.Stat(keyPath); os.IsNotExist(err) {
			t.Error("Key file was not created")
		}

		// Verify directory has correct permissions (0700)
		info, err := os.Stat(tempDir + "/nested/dir/path")
		if err != nil {
			t.Fatalf("Failed to stat directory: %v", err)
		}

		perm := info.Mode().Perm()
		if perm != 0700 {
			t.Errorf("Expected directory permissions 0700, got %04o", perm)
		}

		// Verify the key can be loaded back
		loadedKey, err := LoadPrivateKey(keyPath)
		if err != nil {
			t.Fatalf("Failed to load saved key: %v", err)
		}

		if loadedKey.N.Cmp(key.N) != 0 {
			t.Error("Loaded key doesn't match generated key")
		}
	})

	t.Run("should regenerate if loading existing key fails", func(t *testing.T) {
		tempDir := t.TempDir()
		keyPath := tempDir + "/corrupted_key.pem"

		// Create a corrupted key file
		corruptedContent := []byte("This is not a valid PEM key file")
		err := os.WriteFile(keyPath, corruptedContent, 0600)
		if err != nil {
			t.Fatalf("Failed to create corrupted key file: %v", err)
		}

		// LoadOrGenerateKeypair should regenerate when loading fails
		key, err := LoadOrGenerateKeypair(keyPath, 2048)
		if err != nil {
			t.Fatalf("Failed to regenerate keypair: %v", err)
		}

		if key == nil {
			t.Fatal("Generated key is nil")
		}

		// Verify key is valid
		if err := key.Validate(); err != nil {
			t.Errorf("Generated key is invalid: %v", err)
		}

		// Verify the file was overwritten with valid content
		loadedKey, err := LoadPrivateKey(keyPath)
		if err != nil {
			t.Fatalf("Failed to load regenerated key: %v", err)
		}

		if loadedKey.N.Cmp(key.N) != 0 {
			t.Error("Loaded key doesn't match regenerated key")
		}

		// Verify the file content is now valid PEM
		content, err := os.ReadFile(keyPath)
		if err != nil {
			t.Fatalf("Failed to read key file: %v", err)
		}

		if !bytes.Contains(content, []byte("-----BEGIN RSA PRIVATE KEY-----")) {
			t.Error("File doesn't contain valid PEM header after regeneration")
		}

		if bytes.Equal(content, corruptedContent) {
			t.Error("File content wasn't replaced after regeneration")
		}
	})

	t.Run("should save with correct file permissions (0600)", func(t *testing.T) {
		tempDir := t.TempDir()
		keyPath := tempDir + "/permission_test_key.pem"

		// Generate a new keypair
		key, err := LoadOrGenerateKeypair(keyPath, 2048)
		if err != nil {
			t.Fatalf("Failed to load or generate keypair: %v", err)
		}

		if key == nil {
			t.Fatal("Generated key is nil")
		}

		// Check file permissions
		info, err := os.Stat(keyPath)
		if err != nil {
			t.Fatalf("Failed to stat key file: %v", err)
		}

		perm := info.Mode().Perm()
		if perm != 0600 {
			t.Errorf("Expected file permissions 0600, got %04o", perm)
		}

		// Also test when file already exists with wrong permissions
		keyPath2 := tempDir + "/permission_test_key2.pem"

		// Create file with wrong permissions first
		err = os.WriteFile(keyPath2, []byte("dummy"), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		// LoadOrGenerateKeypair should fix permissions when regenerating
		key2, err := LoadOrGenerateKeypair(keyPath2, 2048)
		if err != nil {
			t.Fatalf("Failed to load or generate keypair: %v", err)
		}

		if key2 == nil {
			t.Fatal("Generated key is nil")
		}

		// Check that permissions are now correct
		info2, err := os.Stat(keyPath2)
		if err != nil {
			t.Fatalf("Failed to stat key file: %v", err)
		}

		perm2 := info2.Mode().Perm()
		if perm2 != 0600 {
			t.Errorf("Expected file permissions to be corrected to 0600, got %04o", perm2)
		}
	})
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	t.Run("should successfully save and load the same key", func(t *testing.T) {
		// Generate multiple keys of different sizes to test
		keySizes := []int{2048, 3072, 4096}

		for _, bits := range keySizes {
			t.Run(fmt.Sprintf("%d-bit", bits), func(t *testing.T) {
				// Generate original key
				originalKey, err := GenerateRSAKeypair(bits)
				if err != nil {
					t.Fatalf("Failed to generate %d-bit RSA keypair: %v", bits, err)
				}

				tmpFile, cleanup := setupTempFile(t)
				defer cleanup()

				// Save the key
				err = SavePrivateKey(originalKey, tmpFile)
				if err != nil {
					t.Fatalf("Failed to save private key: %v", err)
				}

				// Load the key back
				loadedKey, err := LoadPrivateKey(tmpFile)
				if err != nil {
					t.Fatalf("Failed to load private key: %v", err)
				}

				// Verify all components match
				if originalKey.N.Cmp(loadedKey.N) != 0 {
					t.Errorf("%d-bit: Modulus doesn't match", bits)
				}

				if originalKey.E != loadedKey.E {
					t.Errorf("%d-bit: Public exponent doesn't match", bits)
				}

				if originalKey.D.Cmp(loadedKey.D) != 0 {
					t.Errorf("%d-bit: Private exponent doesn't match", bits)
				}

				// Verify primes match
				if len(originalKey.Primes) != len(loadedKey.Primes) {
					t.Errorf("%d-bit: Number of primes doesn't match", bits)
				} else {
					for i := range originalKey.Primes {
						if originalKey.Primes[i].Cmp(loadedKey.Primes[i]) != 0 {
							t.Errorf("%d-bit: Prime[%d] doesn't match", bits, i)
						}
					}
				}

				// Test that both keys can perform the same operations
				// Encrypt with original public key
				message := []byte("Test message for round trip")
				encrypted, err := rsa.EncryptPKCS1v15(rand.Reader, &originalKey.PublicKey, message)
				if err != nil {
					t.Fatalf("%d-bit: Failed to encrypt with original key: %v", bits, err)
				}

				// Decrypt with loaded private key
				decrypted, err := rsa.DecryptPKCS1v15(rand.Reader, loadedKey, encrypted)
				if err != nil {
					t.Fatalf("%d-bit: Failed to decrypt with loaded key: %v", bits, err)
				}

				if !bytes.Equal(message, decrypted) {
					t.Errorf("%d-bit: Decrypted message doesn't match original", bits)
				}

				// Also test the reverse: encrypt with loaded, decrypt with original
				encrypted2, err := rsa.EncryptPKCS1v15(rand.Reader, &loadedKey.PublicKey, message)
				if err != nil {
					t.Fatalf("%d-bit: Failed to encrypt with loaded key: %v", bits, err)
				}

				decrypted2, err := rsa.DecryptPKCS1v15(rand.Reader, originalKey, encrypted2)
				if err != nil {
					t.Fatalf("%d-bit: Failed to decrypt with original key: %v", bits, err)
				}

				if !bytes.Equal(message, decrypted2) {
					t.Errorf("%d-bit: Reverse decrypted message doesn't match original", bits)
				}
			})
		}
	})
}

func setupTempFile(t *testing.T) (string, func()) {
	tmpFile, err := os.CreateTemp("", "test-key-*.pem")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()

	cleanup := func() {
		os.Remove(tmpFile.Name())
	}

	return tmpFile.Name(), cleanup
}