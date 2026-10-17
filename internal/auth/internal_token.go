package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Internal tokens let the gateway act as a user against its own /mcp endpoint, so the
// chat agent's tool calls are authorized and audited as the user, not a service account.
// A token is gwi_<payload>.<mac>: the payload holds the user id and expiry, and the MAC is
// HMAC-SHA256 under a key that exists only in this process's memory. Tokens therefore never
// outlive the process that minted them and need no storage.
const internalScheme = "gwi_"

// ErrInvalidInternalToken means an internal token is malformed, forged, or expired.
var ErrInvalidInternalToken = errors.New("invalid internal token")

// TokenIssuer mints and verifies internal tokens.
type TokenIssuer struct {
	key []byte
	now func() time.Time
}

// NewTokenIssuer returns an issuer with a fresh random key.
func NewTokenIssuer() (*TokenIssuer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return &TokenIssuer{key: key, now: time.Now}, nil
}

// IsInternalToken reports whether a bearer token has the internal-token form.
func IsInternalToken(token string) bool { return strings.HasPrefix(token, internalScheme) }

// Mint returns a token for userID, valid for ttl.
func (t *TokenIssuer) Mint(userID int64, ttl time.Duration) string {
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[:8], uint64(userID))
	binary.BigEndian.PutUint64(payload[8:], uint64(t.now().Add(ttl).Unix()))
	return internalScheme + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(t.mac(payload))
}

// Verify returns the user id an unexpired, authentic token names.
func (t *TokenIssuer) Verify(token string) (int64, error) {
	body, ok := strings.CutPrefix(token, internalScheme)
	if !ok {
		return 0, ErrInvalidInternalToken
	}
	encPayload, encMAC, ok := strings.Cut(body, ".")
	if !ok {
		return 0, ErrInvalidInternalToken
	}
	payload, err1 := base64.RawURLEncoding.Strict().DecodeString(encPayload)
	mac, err2 := base64.RawURLEncoding.Strict().DecodeString(encMAC)
	if err1 != nil || err2 != nil || len(payload) != 16 || !hmac.Equal(mac, t.mac(payload)) {
		return 0, ErrInvalidInternalToken
	}
	if expires := int64(binary.BigEndian.Uint64(payload[8:])); t.now().Unix() >= expires {
		return 0, fmt.Errorf("%w: expired", ErrInvalidInternalToken)
	}
	return int64(binary.BigEndian.Uint64(payload[:8])), nil
}

func (t *TokenIssuer) mac(payload []byte) []byte {
	h := hmac.New(sha256.New, t.key)
	h.Write(payload)
	return h.Sum(nil)
}
