package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"
)

// OAuth token prefixes. They let the verifier tell an OAuth token from an API key or an
// internal token without a lookup, and make a leaked token easy to recognize.
const (
	OAuthAccessPrefix  = "gwo_"
	OAuthRefreshPrefix = "gwr_"
)

// ErrInvalidOAuthToken is returned for an unknown, expired, or wrongly addressed OAuth token.
var ErrInvalidOAuthToken = errors.New("invalid OAuth token")

// OAuthAccess is what an access token grants: a user, until a time, for a resource.
type OAuthAccess struct {
	User      User
	Resource  string
	ExpiresAt time.Time
}

// OAuthTokens looks up access tokens by their hash. An unknown or expired token is
// ErrInvalidOAuthToken.
type OAuthTokens interface {
	OAuthAccess(ctx context.Context, hash []byte) (OAuthAccess, error)
}

// OAuth enables OAuth access tokens in a Verifier. Resource is this server's resource URL;
// a token issued for any other resource is refused (RFC 8707 audience binding).
type OAuth struct {
	Tokens   OAuthTokens
	Resource string
}

// HashToken is how OAuth codes and tokens are stored: they are long random strings, so a
// plain SHA-256 suffices, as for API key secrets.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

func (o *OAuth) access(ctx context.Context, token string) (OAuthAccess, error) {
	if o == nil {
		return OAuthAccess{}, fmt.Errorf("%w: OAuth tokens are not accepted here", ErrInvalidOAuthToken)
	}
	a, err := o.Tokens.OAuthAccess(ctx, HashToken(token))
	if err != nil {
		return a, err
	}
	if a.Resource != o.Resource {
		return a, fmt.Errorf("%w: issued for %s", ErrInvalidOAuthToken, a.Resource)
	}
	return a, nil
}

func isOAuthAccess(token string) bool { return strings.HasPrefix(token, OAuthAccessPrefix) }
