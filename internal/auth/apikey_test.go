package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireAPIKey(t *testing.T) {
	protected := RequireAPIKey("X-Api-Key", "secret")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	cases := []struct {
		name, key string
		set       bool
		want      int
	}{
		{"right key", "secret", true, http.StatusTeapot},
		{"wrong key", "nope", true, http.StatusUnauthorized},
		{"missing header", "", false, http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			if tc.set {
				req.Header.Set("X-Api-Key", tc.key)
			}
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestRequireAPIKeyRefusesEmptyKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("RequireAPIKey accepted an empty key")
		}
	}()
	RequireAPIKey("X-Api-Key", "")
}
