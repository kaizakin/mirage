// Command currency-mcp serves fixed-rate currency conversion as MCP tools over streamable
// HTTP at /mcp. Callers authenticate with a static X-Api-Key header, which demonstrates
// header-style upstream credentials; /healthz and /readyz are open.
//
// It reads CURRENCY_API_KEY, required, and LISTEN_ADDR, which defaults to ":8080".
package main

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/currencymcp"
	"github.com/kaizakin/mirage/internal/platform"
)

const version = "0.1.0"

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("currency-mcp stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	env, err := platform.RequireEnv("CURRENCY_API_KEY")
	if err != nil {
		return err
	}

	server := currencymcp.NewServer(version)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	mux.Handle("/mcp", auth.RequireAPIKey("X-Api-Key", env["CURRENCY_API_KEY"])(mcpHandler))
	platform.RegisterHealth(mux, func(context.Context) error { return nil })

	slog.Info("starting currency-mcp", "version", version)
	return platform.Serve(cmp.Or(os.Getenv("LISTEN_ADDR"), ":8080"), mux)
}
