package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"runtime"

	"golang.org/x/crypto/argon2"
)

// GenerateRandomBytes generates n random bytes.
func GenerateRandomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	_, err := rand.Read(b)
	if err != nil {
		return nil, fmt.Errorf("failed to generate random bytes: %w", err)
	}
	return b, nil
}

// GenerateRandomURLEncodedString generates a URL-safe base64 encoded string of n random bytes.
func GenerateRandomURLEncodedString(n int) (string, error) {
	b, err := GenerateRandomBytes(n)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

// NewArgon2IDKey generates an encryption key and salt for each file
// The Argon2id variant with t=3 and 64 MiB memory is the SECOND RECOMMENDED option
// RFC 9106 Argon2 Memory-Hard Function for Password Hashing and Proof-of-Work Applications
// Use SECOND RECOMMENDED option since multiple goroutines are used in file processing (FIRST RECOMMENDED is 2 GiB memory)
func NewArgon2IDKey(secret string, salt []byte) []byte {
	return argon2.IDKey([]byte(secret), salt, 3, 64*1024, uint8(runtime.NumCPU()), 32)
}

// NewAESGCMCipher creates a new AES-GCM cipher with the given key.
func NewAESGCMCipher(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}
	aesgcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}
	return aesgcm, nil
}
