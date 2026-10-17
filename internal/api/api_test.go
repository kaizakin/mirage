package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kaizakin/mirage/internal/auth"
)

func TestSameOriginBlocksForgeableRequests(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := sameOrigin(ok)
	cases := []struct {
		name, method, origin, contentType, body string
		want                                    int
	}{
		{"same-origin JSON", http.MethodPost, "http://dash.example", "application/json", "{}", http.StatusNoContent},
		{"JSON with charset", http.MethodPost, "http://dash.example", "application/json; charset=utf-8", "{}", http.StatusNoContent},
		{"no Origin, as from curl", http.MethodPost, "", "application/json", "{}", http.StatusNoContent},
		{"cross-site JSON", http.MethodPost, "http://evil.example", "application/json", "{}", http.StatusForbidden},
		{"cross-site form post", http.MethodPost, "http://evil.example", "application/x-www-form-urlencoded", "a=b", http.StatusForbidden},
		{"form post without Origin", http.MethodPost, "", "application/x-www-form-urlencoded", "a=b", http.StatusUnsupportedMediaType},
		{"text/plain body", http.MethodPatch, "", "text/plain", "{}", http.StatusUnsupportedMediaType},
		{"bodiless DELETE", http.MethodDelete, "", "", "", http.StatusNoContent},
		{"GET from anywhere", http.MethodGet, "http://evil.example", "", "", http.StatusNoContent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, "http://dash.example/api/servers", strings.NewReader(tc.body))
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d", rec.Code, tc.want)
			}
		})
	}
}

func TestRequireAdminRefusesMembers(t *testing.T) {
	h := requireAdmin(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	for role, want := range map[string]int{"admin": http.StatusNoContent, "member": http.StatusForbidden, "": http.StatusForbidden} {
		req := httptest.NewRequest(http.MethodGet, "/api/servers", nil)
		req = req.WithContext(context.WithValue(req.Context(), userKey{}, auth.User{ID: 1, Role: role}))
		rec := httptest.NewRecorder()
		h(rec, req)
		if rec.Code != want {
			t.Errorf("role %q: status = %d, want %d", role, rec.Code, want)
		}
	}
}
