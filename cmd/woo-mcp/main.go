// Command woo-mcp serves a WooCommerce store's products and orders as MCP tools over
// streamable HTTP at /mcp, behind a static bearer token, with /healthz and /readyz probes.
//
// It reads WOO_BASE_URL, WOO_USERNAME, WOO_PASSWORD, and MCP_BEARER_TOKEN, all required,
// and LISTEN_ADDR, which defaults to ":8080".
package main

import (
	"cmp"
	"log/slog"
	"net/http"
	"os"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/platform"
	"github.com/kaizakin/mirage/internal/woo"
	"github.com/kaizakin/mirage/internal/woomcp"
)

const version = "0.1.0"

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

	client := woo.New(env["WOO_BASE_URL"], env["WOO_USERNAME"], env["WOO_PASSWORD"])
	server := woomcp.NewServer(client, version)
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)

	mux := http.NewServeMux()
	mux.Handle("/mcp", sdkauth.RequireBearerToken(auth.StaticToken(env["MCP_BEARER_TOKEN"]), nil)(mcpHandler))
	platform.RegisterHealth(mux, client.Ping)

	slog.Info("starting woo-mcp", "store", env["WOO_BASE_URL"], "version", version)
	ctx, stop := platform.SignalContext()
	defer stop()
	return platform.Serve(ctx, cmp.Or(os.Getenv("LISTEN_ADDR"), ":8080"), mux)
}
