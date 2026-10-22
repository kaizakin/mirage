package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const tokenInfoUser = "mirage.user"

// ErrUserNotFound is returned by a UserStore when no user has the given id.
var ErrUserNotFound = errors.New("user not found")

// UserStore loads users by id. It returns ErrUserNotFound when none matches.
type UserStore interface {
	UserByID(ctx context.Context, id int64) (User, error)
}

// Internal enables internal tokens in a Verifier: Issuer checks them and Users loads the
// user each one names, so a token for a deleted user stops working at once.
type Internal struct {
	Issuer *TokenIssuer
	Users  UserStore
}

// VerifierOptions turns on the credentials a Verifier accepts beyond API keys.
type VerifierOptions struct {
	// Internal accepts the chat agent's internal tokens.
	Internal *Internal
	// OAuth accepts access tokens from the gateway's authorization server.
	OAuth *OAuth
}

// Verifier adapts credential checks to the SDK's bearer middleware, which runs on every
// request. It accepts API keys and, as opts allows, internal tokens and OAuth access tokens. A
// bad credential becomes sdkauth.ErrInvalidToken (401); a store failure stays a plain error
// (500), so an outage is not reported as a revoked key. Setting UserID lets the SDK refuse
// a session presented by a different user than the one who opened it.
func Verifier(keys KeyStore, opts VerifierOptions) sdkauth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		var user User
		var err error
		expires := noExpiry
		switch {
		case IsInternalToken(token):
			user, err = opts.Internal.user(ctx, token)
		case isOAuthAccess(token):
			var a OAuthAccess
			a, err = opts.OAuth.access(ctx, token)
			user, expires = a.User, a.ExpiresAt
		default:
			user, err = VerifyKey(ctx, keys, token)
		}
		// The middleware writes the error into the response body, so callers get a generic
		// message: the reason (unknown id, wrong secret, revoked) or a database error would
		// tell an unauthenticated caller too much. Operators read it in the log.
		if errors.Is(err, ErrInvalidKey) || errors.Is(err, ErrInvalidInternalToken) || errors.Is(err, ErrInvalidOAuthToken) {
			slog.InfoContext(ctx, "rejected credential", "reason", err)
			return nil, fmt.Errorf("%w: invalid API key", sdkauth.ErrInvalidToken)
		}
		if err != nil {
			slog.ErrorContext(ctx, "verifying credential", "err", err)
			return nil, errors.New("authentication is unavailable")
		}
		return &sdkauth.TokenInfo{
			UserID:     strconv.FormatInt(user.ID, 10),
			Expiration: expires,
			Extra:      map[string]any{tokenInfoUser: user},
		}, nil
	}
}

func (in *Internal) user(ctx context.Context, token string) (User, error) {
	if in == nil {
		return User{}, fmt.Errorf("%w: internal tokens are not accepted here", ErrInvalidInternalToken)
	}
	id, err := in.Issuer.Verify(token)
	if err != nil {
		return User{}, err
	}
	u, err := in.Users.UserByID(ctx, id)
	if errors.Is(err, ErrUserNotFound) {
		return User{}, fmt.Errorf("%w: user %d no longer exists", ErrInvalidInternalToken, id)
	}
	return u, err
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
