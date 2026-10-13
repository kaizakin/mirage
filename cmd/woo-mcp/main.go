// Command woo-mcp serves a WooCommerce store's products and orders as MCP tools over
// streamable HTTP at /mcp, behind a static bearer token, with /healthz and /readyz probes.
//
// It reads WOO_BASE_URL, WOO_USERNAME, WOO_PASSWORD, and MCP_BEARER_TOKEN, all required,
// and LISTEN_ADDR, which defaults to ":8080".
package main

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/platform"
	"github.com/kaizakin/mirage/internal/woo"
	"github.com/kaizakin/mirage/internal/woomcp"
)

const (
	version         = "0.1.0"
	shutdownTimeout = 10 * time.Second
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("woo-mcp stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	env, err := platform.RequireEnv("WOO_BASE_URL", "WOO_USERNAME", "WOO_PASSWORD", "MCP_BEARER_TOKEN")
	if err != nil {
		return err
	}
	addr := cmp.Or(os.Getenv("LISTEN_ADDR"), ":8080")

	client := woo.New(env["WOO_BASE_URL"], env["WOO_USERNAME"], env["WOO_PASSWORD"])
	server := woomcp.NewServer(client, version)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	shuttingDown, beginShutdown := context.WithCancel(context.Background())
	defer beginShutdown()

	mux := http.NewServeMux()
	mux.Handle("/mcp", sdkauth.RequireBearerToken(auth.StaticToken(env["MCP_BEARER_TOKEN"]), nil)(
		endStreamsOn(shuttingDown, mcpHandler)))
	platform.RegisterHealth(mux, client.Ping)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	srv.RegisterOnShutdown(beginShutdown)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	slog.Info("listening", "addr", addr, "store", env["WOO_BASE_URL"], "version", version)

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

// endStreamsOn ends GET requests, the standalone SSE streams a client holds open for
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
