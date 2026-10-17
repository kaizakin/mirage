package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// API keys look like gw_<id>_<secret>. The id is public: it names the database row and is
// stored as api_keys.key_prefix. Only sha256(secret) is stored, so a leaked table yields no
// usable key. The secret's base64url alphabet includes '_', so keys are parsed by width,
// not by splitting on underscores.
const (
	keyScheme    = "gw_"
	keyIDLen     = 8
	secretBytes  = 32
	secretEncLen = 43 // base64url without padding of secretBytes
	keyLen       = len(keyScheme) + keyIDLen + 1 + secretEncLen
)

var keyIDEncoding = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// ErrInvalidKey means an API key is malformed, unknown, wrong, or revoked. Callers get no
// detail beyond the wrapped reason, which is for logs.
var ErrInvalidKey = errors.New("invalid API key")

// ErrKeyNotFound is returned by a KeyStore when no key has the given id.
var ErrKeyNotFound = errors.New("API key not found")

// User is the identity an API key authenticates as.
type User struct {
	ID    int64
	Email string
	Role  string
}

// KeyRecord is the stored half of an API key, joined with its owner.
type KeyRecord struct {
	User       User
	SecretHash []byte
	Revoked    bool
}

// KeyStore loads stored keys by id. It returns ErrKeyNotFound when none matches.
type KeyStore interface {
	APIKeyByID(ctx context.Context, id string) (KeyRecord, error)
}

// NewKey is a freshly generated API key. Plaintext is shown to its owner once and never
// stored; ID and SecretHash are what the database keeps.
type NewKey struct {
	Plaintext  string
	ID         string
	SecretHash []byte
}

// GenerateKey returns a new random API key.
func GenerateKey() (NewKey, error) {
	idBytes := make([]byte, 5) // 40 bits encode to exactly keyIDLen base32 characters
	secret := make([]byte, secretBytes)
	if _, err := rand.Read(idBytes); err != nil {
		return NewKey{}, err
	}
	if _, err := rand.Read(secret); err != nil {
		return NewKey{}, err
	}
	id := keyIDEncoding.EncodeToString(idBytes)
	hash := sha256.Sum256(secret)
	return NewKey{
		Plaintext:  keyScheme + id + "_" + base64.RawURLEncoding.EncodeToString(secret),
		ID:         id,
		SecretHash: hash[:],
	}, nil
}

// HashKey returns the id and stored hash for a plaintext key, for seeding a key generated
// elsewhere. It returns ErrInvalidKey if the key is malformed.
func HashKey(plaintext string) (id string, secretHash []byte, err error) {
	id, secret, err := parseKey(plaintext)
	if err != nil {
		return "", nil, err
	}
	hash := sha256.Sum256(secret)
	return id, hash[:], nil
}

// VerifyKey returns the user a plaintext API key authenticates as. Every failure wraps
// ErrInvalidKey except a store error other than ErrKeyNotFound, which is returned as is so
// an outage is not reported as a bad key.
func VerifyKey(ctx context.Context, store KeyStore, plaintext string) (User, error) {
	id, secret, err := parseKey(plaintext)
	if err != nil {
		return User{}, err
	}
	rec, err := store.APIKeyByID(ctx, id)
	if errors.Is(err, ErrKeyNotFound) {
		return User{}, fmt.Errorf("%w: unknown key id %s", ErrInvalidKey, id)
	}
	if err != nil {
		return User{}, err
	}
	hash := sha256.Sum256(secret)
	if subtle.ConstantTimeCompare(hash[:], rec.SecretHash) != 1 {
		return User{}, fmt.Errorf("%w: wrong secret for key %s", ErrInvalidKey, id)
	}
	if rec.Revoked {
		return User{}, fmt.Errorf("%w: key %s is revoked", ErrInvalidKey, id)
	}
	if t, ok := store.(KeyToucher); ok {
		// Recording use is best effort: a failed write must not fail an authenticated request.
		_ = t.TouchAPIKey(ctx, id)
	}
	return rec.User, nil
}

// KeyToucher is implemented by stores that record when a key was last used. VerifyKey
// calls it only after a key verifies, so failed guesses never mark a key used.
type KeyToucher interface {
	TouchAPIKey(ctx context.Context, id string) error
}

func parseKey(s string) (id string, secret []byte, err error) {
	if len(s) != keyLen || !strings.HasPrefix(s, keyScheme) || s[len(keyScheme)+keyIDLen] != '_' {
		return "", nil, fmt.Errorf("%w: not of the form gw_<id>_<secret>", ErrInvalidKey)
	}
	id = s[len(keyScheme) : len(keyScheme)+keyIDLen]
	if _, err := keyIDEncoding.DecodeString(id); err != nil {
		return "", nil, fmt.Errorf("%w: malformed key id", ErrInvalidKey)
	}
	// Strict: each key has exactly one spelling, so no near-duplicate decodes to the same secret.
	secret, err = base64.RawURLEncoding.Strict().DecodeString(s[len(keyScheme)+keyIDLen+1:])
	if err != nil || len(secret) != secretBytes {
		return "", nil, fmt.Errorf("%w: malformed secret", ErrInvalidKey)
	}
	return id, secret, nil
}
