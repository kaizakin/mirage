package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

type fakeOAuthTokens map[string]OAuthAccess

func (f fakeOAuthTokens) OAuthAccess(_ context.Context, hash []byte) (OAuthAccess, error) {
	if a, ok := f[string(hash)]; ok {
		return a, nil
	}
	return OAuthAccess{}, ErrInvalidOAuthToken
}

func TestVerifierAcceptsOAuthTokensOnlyForThisResource(t *testing.T) {
	ada := User{ID: 7, Email: "ada@example.com", Role: "member"}
	expires := time.Now().Add(time.Hour).Truncate(time.Second)
	tokens := fakeOAuthTokens{
		string(HashToken("gwo_good")):  {User: ada, Resource: "https://gw.example/mcp", ExpiresAt: expires},
		string(HashToken("gwo_other")): {User: ada, Resource: "https://elsewhere.example/mcp", ExpiresAt: expires},
	}
	verify := Verifier(fakeKeys{}, VerifierOptions{OAuth: &OAuth{Tokens: tokens, Resource: "https://gw.example/mcp"}})

	info, err := verify(t.Context(), "gwo_good", nil)
	if err != nil {
		t.Fatal(err)
	}
	if u, _ := UserFromTokenInfo(info); u != ada || !info.Expiration.Equal(expires) {
		t.Fatalf("token info = %+v; want ada with the token's own expiry", info)
	}
	for _, token := range []string{"gwo_other", "gwo_unknown"} {
		if _, err := verify(t.Context(), token, nil); !errors.Is(err, sdkauth.ErrInvalidToken) {
			t.Errorf("%s: err = %v, want ErrInvalidToken", token, err)
		}
	}
	if _, err := Verifier(fakeKeys{}, VerifierOptions{})(t.Context(), "gwo_good", nil); !errors.Is(err, sdkauth.ErrInvalidToken) {
		t.Fatalf("OAuth token on a verifier without OAuth: err = %v, want ErrInvalidToken", err)
	}
}
