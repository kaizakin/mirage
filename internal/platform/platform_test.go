package platform

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReadyz(t *testing.T) {
	cases := []struct {
		name     string
		ready    error
		wantCode int
		wantBody string
	}{
		{"ready", nil, http.StatusOK, "ok"},
		{"not ready", errors.New("store unreachable"), http.StatusServiceUnavailable, "store unreachable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mux := http.NewServeMux()
			RegisterHealth(mux, func(context.Context) error { return tc.ready })
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
			if rec.Code != tc.wantCode || !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("readyz = %d %q, want %d containing %q", rec.Code, rec.Body, tc.wantCode, tc.wantBody)
			}
		})
	}
}

func TestHealthzIgnoresReadiness(t *testing.T) {
	mux := http.NewServeMux()
	RegisterHealth(mux, func(context.Context) error { return errors.New("down") })
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d, want 200", rec.Code)
	}
}

func TestRequireEnvListsEveryMissingVariable(t *testing.T) {
	t.Setenv("MIRAGE_TEST_SET", "v")
	t.Setenv("MIRAGE_TEST_EMPTY", "")
	_, err := RequireEnv("MIRAGE_TEST_SET", "MIRAGE_TEST_EMPTY", "MIRAGE_TEST_UNSET")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, name := range []string{"MIRAGE_TEST_EMPTY", "MIRAGE_TEST_UNSET"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("error %q does not name %s", err, name)
		}
	}
	if strings.Contains(err.Error(), "MIRAGE_TEST_SET") {
		t.Errorf("error %q names a variable that is set", err)
	}
}

func TestRequireEnvReturnsValues(t *testing.T) {
	t.Setenv("MIRAGE_TEST_A", "a")
	t.Setenv("MIRAGE_TEST_B", "b")
	got, err := RequireEnv("MIRAGE_TEST_A", "MIRAGE_TEST_B")
	if err != nil {
		t.Fatal(err)
	}
	if got["MIRAGE_TEST_A"] != "a" || got["MIRAGE_TEST_B"] != "b" {
		t.Fatalf("values = %v", got)
	}
}
