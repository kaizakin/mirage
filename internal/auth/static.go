// Package auth verifies credentials presented to mirage services.
package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

// The SDK middleware rejects a TokenInfo without an expiration, and a static token never expires.
var noExpiry = time.Date(9999, time.December, 31, 0, 0, 0, 0, time.UTC)

// StaticToken returns a verifier that accepts exactly token. It panics if token is empty,
// because an empty configured token must never authenticate anything.
func StaticToken(token string) sdkauth.TokenVerifier {
	if token == "" {
		panic("auth: StaticToken called with an empty token")
	}
	want := []byte(token)
	return func(_ context.Context, got string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			return nil, fmt.Errorf("bearer token mismatch: %w", sdkauth.ErrInvalidToken)
		}
		return &sdkauth.TokenInfo{Expiration: noExpiry}, nil
	}
}
