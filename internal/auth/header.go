package auth

import (
	"crypto/subtle"
	"net/http"
)

// RequireHeader returns middleware that rejects, with 401, any request whose header
// does not carry exactly value. It panics if value is empty, because an empty configured
// value must never authenticate anything.
func RequireHeader(header, value string) func(http.Handler) http.Handler {
	if value == "" {
		panic("auth: RequireHeader called with an empty value")
	}
	want := []byte(value)
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
