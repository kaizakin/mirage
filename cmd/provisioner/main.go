// Command provisioner turns the stores users ask for in the dashboard into running stores.
// Each store is the store Helm chart installed in its own namespace; once it is up, its MCP
// server is registered in the gateway. See internal/provisioner.
//
// It reads DATABASE_URL and SECRETBOX_KEY, both required and shared with the gateway;
// STORE_CHART, the chart's directory (default /charts/store); GATEWAY_NAMESPACE, where the
// gateway runs (default mirage); PROVISION_TIMEOUT, how long a store may take to become ready
// before it fails (default 10m); and LISTEN_ADDR for its health endpoints (default :8080).
package main

import (
	"cmp"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/kaizakin/mirage/internal/platform"
	"github.com/kaizakin/mirage/internal/provisioner"
	"github.com/kaizakin/mirage/internal/secretbox"
	"github.com/kaizakin/mirage/internal/store"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("provisioner stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	env, err := platform.RequireEnv("DATABASE_URL", "SECRETBOX_KEY")
	if err != nil {
		return err
	}
	box, err := secretbox.NewFromBase64(env["SECRETBOX_KEY"])
	if err != nil {
		return err
	}
	timeout, err := time.ParseDuration(cmp.Or(os.Getenv("PROVISION_TIMEOUT"), "10m"))
	if err != nil || timeout <= 0 {
		return fmt.Errorf("PROVISION_TIMEOUT must be a positive duration such as 10m, got %q", os.Getenv("PROVISION_TIMEOUT"))
	}
	cluster, err := provisioner.NewKube(cmp.Or(os.Getenv("STORE_CHART"), "/charts/store"))
	if err != nil {
		return err
	}

	ctx, stop := platform.SignalContext()
	defer stop()
	db, err := store.OpenWhenReady(ctx, env["DATABASE_URL"])
	if err != nil {
		return err
	}
	defer db.Close()

	p := provisioner.New(store.New(db), cluster, box, provisioner.Config{
		Timeout:          timeout,
		GatewayNamespace: cmp.Or(os.Getenv("GATEWAY_NAMESPACE"), "mirage"),
	})
	mux := http.NewServeMux()
	platform.RegisterHealth(mux, db.PingContext)

	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return platform.Serve(ctx, cmp.Or(os.Getenv("LISTEN_ADDR"), ":8080"), mux) })
	g.Go(func() error { return p.Run(ctx) })
	slog.Info("provisioning stores", "timeout", timeout)
	return g.Wait()
}
