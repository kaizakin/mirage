// Package secretbox seals upstream credentials for storage with AES-256-GCM.
//
// A sealed value is nonce || ciphertext || tag. The associated data passed to Encrypt and Decrypt
// binds a value to its context (for example the server's slug): it is authenticated but not
// stored, so a value copied into another row fails to open.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// KeySize is the required key length in bytes (AES-256).
const KeySize = 32

// ErrOpen means a sealed value is malformed, was tampered with, or was sealed under a
// different key or associated data. The cases are deliberately indistinguishable.
var ErrOpen = errors.New("secretbox: cannot open sealed value")

// Box seals and opens values under one key. It is safe for concurrent use.
type Box struct {
	aead cipher.AEAD
}

// New returns a Box for a KeySize-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: key is %d bytes, want %d", len(key), KeySize)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// NewFromBase64 returns a Box for a standard-base64-encoded KeySize-byte key, the form
// the key takes in a Secret or environment variable.
func NewFromBase64(encoded string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("secretbox: key is not valid base64: %w", err)
	}
	return New(key)
}

// Encrypt seals plaintext, authenticating additionalData alongside it.
func (b *Box) Encrypt(plaintext, additionalData []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize(), b.aead.NonceSize()+len(plaintext)+b.aead.Overhead())
	// A fresh random nonce per value; GCM must never reuse a nonce under one key.
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, additionalData), nil
}

// Decrypt opens a value from Encrypt. It returns ErrOpen unless the value is intact and
// was sealed under this key with the same additionalData.
func (b *Box) Decrypt(sealed, additionalData []byte) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return nil, ErrOpen
	}
	plaintext, err := b.aead.Open(nil, sealed[:n], sealed[n:], additionalData)
	if err != nil {
		return nil, ErrOpen
	}
	return plaintext, nil
}
