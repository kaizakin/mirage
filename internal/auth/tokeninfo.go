package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const tokenInfoUser = "mirage.user"

// KeyVerifier adapts API key verification to the SDK's bearer middleware, which runs on
// every request. A bad key becomes sdkauth.ErrInvalidToken (401); a store failure stays a
// plain error (500), so an outage is not reported as a revoked key. Setting UserID lets the
// SDK refuse a session presented by a different user than the one who opened it.
func KeyVerifier(store KeyStore) sdkauth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		user, err := VerifyKey(ctx, store, token)
		if errors.Is(err, ErrInvalidKey) {
			return nil, fmt.Errorf("%w: %w", sdkauth.ErrInvalidToken, err)
		}
		if err != nil {
			return nil, err
		}
		return &sdkauth.TokenInfo{
			UserID:     strconv.FormatInt(user.ID, 10),
			Expiration: noExpiry,
			Extra:      map[string]any{tokenInfoUser: user},
		}, nil
	}
}

// UserFromTokenInfo returns the user a request authenticated as, from the token info the
// SDK's bearer middleware attaches to it.
func UserFromTokenInfo(ti *sdkauth.TokenInfo) (User, bool) {
	if ti == nil {
		return User{}, false
	}
	u, ok := ti.Extra[tokenInfoUser].(User)
	return u, ok
}
