package platform

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/signal"
	"syscall"
	"time"
)

const shutdownTimeout = 10 * time.Second

// Serve runs handler on addr until SIGINT or SIGTERM, then shuts down gracefully:
// in-flight requests get up to ten seconds to finish.
func Serve(addr string, handler http.Handler) error {
	shuttingDown, beginShutdown := context.WithCancel(context.Background())
	defer beginShutdown()

	srv := &http.Server{
		Addr:              addr,
		Handler:           endStreamsOn(shuttingDown, handler),
		ReadHeaderTimeout: 10 * time.Second,
	}
	srv.RegisterOnShutdown(beginShutdown)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", addr)

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}

	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// endStreamsOn ends GET requests, the standalone SSE streams an MCP client holds open for
// server-initiated messages, once done is canceled. They never go idle on their own, so
// without this http.Server.Shutdown would wait out its whole timeout. POSTs carry tool
// calls and are left to finish.
func endStreamsOn(done context.Context, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			ctx, cancel := context.WithCancel(r.Context())
			defer cancel()
			defer context.AfterFunc(done, cancel)()
			r = r.WithContext(ctx)
		}
		next.ServeHTTP(w, r)
	})
}
