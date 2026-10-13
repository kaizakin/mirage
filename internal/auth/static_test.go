package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

func TestStaticTokenThroughMiddleware(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := sdkauth.RequireBearerToken(StaticToken("s3cret"), nil)(ok)

	cases := []struct {
		name, header string
		want         int
	}{
		{"right token", "Bearer s3cret", http.StatusNoContent},
		{"wrong token", "Bearer s3cres", http.StatusUnauthorized},
		{"token prefix", "Bearer s3cre", http.StatusUnauthorized},
		{"no header", "", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tc.want, rec.Body)
			}
		})
	}
}

func TestStaticTokenRejectsWithInvalidToken(t *testing.T) {
	_, err := StaticToken("s3cret")(t.Context(), "nope", nil)
	if !errors.Is(err, sdkauth.ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

func TestStaticTokenRefusesEmptyToken(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("StaticToken(\"\") did not panic")
		}
	}()
	StaticToken("")
}
