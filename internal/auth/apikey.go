package auth

import (
	"crypto/subtle"
	"net/http"
)

// RequireAPIKey returns middleware that rejects, with 401, any request whose header
// does not carry exactly key. It panics if key is empty, because an empty configured
// key must never authenticate anything.
func RequireAPIKey(header, key string) func(http.Handler) http.Handler {
	if key == "" {
		panic("auth: RequireAPIKey called with an empty key")
	}
	want := []byte(key)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get(header)), want) != 1 {
				http.Error(w, "missing or wrong "+header, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
