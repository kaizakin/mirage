package store

import (
	"errors"
	"testing"
	"time"

	"github.com/kaizakin/mirage/internal/auth"
)

func TestLiveOAuthCodesAndRefreshTokensWorkOnceAndRevokeWithTheirGrant(t *testing.T) {
	s := migrated(t)
	ada, _ := s.CreateUser(t.Context(), "ada@example.com", "h", "member")
	client := OAuthClient{ID: "mcp_1", Name: "Claude Code", RedirectURIs: []string{"http://localhost:5555/callback", "https://app.example/cb"}}
	if err := s.CreateOAuthClient(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	if got, err := s.OAuthClientByID(t.Context(), "mcp_1"); err != nil || len(got.RedirectURIs) != 2 || got.RedirectURIs[1] != "https://app.example/cb" {
		t.Fatalf("client = %+v, %v", got, err)
	}

	code := []byte("code-hash")
	if err := s.CreateOAuthCode(t.Context(), OAuthCode{Hash: code, UserID: ada.ID, ClientID: "mcp_1", Resource: "https://gw/mcp",
		RedirectURI: "http://localhost:5555/callback", CodeChallenge: "chal", ExpiresAt: time.Now().Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	grant, redirect, challenge, err := s.RedeemOAuthCode(t.Context(), code)
	if err != nil || grant.UserID != ada.ID || grant.ClientID != "mcp_1" || redirect != "http://localhost:5555/callback" || challenge != "chal" {
		t.Fatalf("redeem = %+v %q %q, %v", grant, redirect, challenge, err)
	}
	if _, _, _, err := s.RedeemOAuthCode(t.Context(), code); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second redeem: err = %v, want ErrNotFound", err)
	}

	access, refresh := []byte("access-hash"), []byte("refresh-hash")
	if err := s.StoreOAuthTokens(t.Context(), grant.ID,
		IssuedToken{Hash: access, Kind: "access", ExpiresAt: time.Now().Add(time.Hour)},
		IssuedToken{Hash: refresh, Kind: "refresh", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if a, err := s.OAuthAccess(t.Context(), access); err != nil || a.User.Email != "ada@example.com" || a.Resource != "https://gw/mcp" {
		t.Fatalf("access = %+v, %v", a, err)
	}
	if _, err := s.OAuthAccess(t.Context(), refresh); !errors.Is(err, auth.ErrInvalidOAuthToken) {
		t.Fatalf("a refresh token used as access: err = %v", err)
	}
	if _, err := s.RedeemOAuthRefresh(t.Context(), refresh); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RedeemOAuthRefresh(t.Context(), refresh); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reused refresh: err = %v, want ErrNotFound", err)
	}

	apps, err := s.ConnectedApps(t.Context(), ada.ID)
	if err != nil || len(apps) != 1 || apps[0].ClientName != "Claude Code" || apps[0].LastUsedAt == nil {
		t.Fatalf("connected apps = %+v, %v", apps, err)
	}
	if err := s.RevokeGrant(t.Context(), ada.ID+1, apps[0].GrantID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("someone else revoking ada's grant: err = %v", err)
	}
	if err := s.RevokeGrant(t.Context(), ada.ID, apps[0].GrantID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OAuthAccess(t.Context(), access); !errors.Is(err, auth.ErrInvalidOAuthToken) {
		t.Fatalf("access token after revoking its grant: err = %v", err)
	}
	if err := s.PruneOAuth(t.Context()); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err := s.OAuthClientByID(t.Context(), "mcp_1"); err != nil {
		t.Fatalf("a client registered just now must survive pruning: %v", err)
	}
}
