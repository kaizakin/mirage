package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

func TestKeyVerifierThroughBearerMiddleware(t *testing.T) {
	store := fakeKeys{}
	valid := issue(t, store, false)
	revoked := issue(t, store, true)

	var seen User
	protected := sdkauth.RequireBearerToken(KeyVerifier(store), nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen, _ = UserFromTokenInfo(sdkauth.TokenInfoFromContext(r.Context()))
		w.WriteHeader(http.StatusNoContent)
	}))
	cases := []struct {
		name, header string
		want         int
	}{
		{"valid key", "Bearer " + valid, http.StatusNoContent},
		{"revoked key", "Bearer " + revoked, http.StatusUnauthorized},
		{"malformed key", "Bearer gw_nope", http.StatusUnauthorized},
		{"no header", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seen = User{}
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
			if tc.want == http.StatusNoContent && seen != ada {
				t.Fatalf("handler saw user %+v, want %+v", seen, ada)
			}
		})
	}
}

func TestKeyVerifierReportsOutageAsServerError(t *testing.T) {
	k, _ := GenerateKey()
	_, err := KeyVerifier(brokenStore{})(t.Context(), k.Plaintext, nil)
	if err == nil || errors.Is(err, sdkauth.ErrInvalidToken) {
		t.Fatalf("err = %v, want a non-ErrInvalidToken error so the middleware answers 500", err)
	}
}

func TestRejectionsRevealNothingToTheCaller(t *testing.T) {
	store := fakeKeys{}
	revoked := issue(t, store, true)
	unknown, _ := GenerateKey()
	cases := map[string]struct {
		store  KeyStore
		key    string
		status int
	}{
		"revoked":      {store, revoked, http.StatusUnauthorized},
		"unknown id":   {store, unknown.Plaintext, http.StatusUnauthorized},
		"store outage": {brokenStore{}, unknown.Plaintext, http.StatusInternalServerError},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			protected := sdkauth.RequireBearerToken(KeyVerifier(tc.store), nil)(http.NotFoundHandler())
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("Authorization", "Bearer "+tc.key)
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)
			body := rec.Body.String()
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			for _, secret := range []string{"revoked", "unknown", "secret", "connection refused", unknown.ID} {
				if strings.Contains(body, secret) {
					t.Fatalf("response body %q reveals %q", body, secret)
				}
			}
		})
	}
}
