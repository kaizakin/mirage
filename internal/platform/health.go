package platform

import (
	"context"
	"io"
	"net/http"
	"time"
)

const readyTimeout = 3 * time.Second

// RegisterHealth registers GET /healthz, which always answers 200, and GET /readyz,
// which answers 200 when ready returns nil within three seconds and 503 with the error otherwise.
func RegisterHealth(mux *http.ServeMux, ready func(context.Context) error) {
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), readyTimeout)
		defer cancel()
		if err := ready(ctx); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
}
