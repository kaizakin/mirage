// Command gateway fronts every upstream MCP server behind one authenticated endpoint.
//
// It reads DATABASE_URL and SECRETBOX_KEY (base64, 32 bytes), both required; LISTEN_ADDR,
// which defaults to ":8080"; and SEED_SERVERS, an optional JSON list of upstreams to create
// on first start, each naming the environment variable that holds its credential:
//
//	[{"slug": "woo", "url": "http://woo-mcp:8080/mcp", "auth_type": "bearer", "credential_env": "WOO_MCP_TOKEN"}]
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/kaizakin/mirage/internal/gateway/upstream"
	"github.com/kaizakin/mirage/internal/platform"
	"github.com/kaizakin/mirage/internal/secretbox"
	"github.com/kaizakin/mirage/internal/store"
)

// serverPollInterval is how quickly the gateway notices servers enabled, disabled, or edited in the database.
const serverPollInterval = 5 * time.Second

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	if err := run(); err != nil {
		slog.Error("gateway stopped", "err", err)
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

	ctx, stop := platform.SignalContext()
	defer stop()

	db, err := store.Open(ctx, env["DATABASE_URL"])
	if err != nil {
		return err
	}
	defer db.Close()
	if err := store.Migrate(ctx, db); err != nil {
		return err
	}
	st := store.New(db)
	if err := seedServers(ctx, st, box, os.Getenv("SEED_SERVERS")); err != nil {
		return err
	}

	upstreams := upstream.NewManager(box, upstream.Options{})
	defer upstreams.Close()

	mux := http.NewServeMux()
	platform.RegisterHealth(mux, db.PingContext)

	// Every job shares ctx: a signal, or any job failing, stops them all.
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return platform.Serve(ctx, cmp.Or(os.Getenv("LISTEN_ADDR"), ":8080"), mux) })
	g.Go(func() error { return syncServers(ctx, st, upstreams) })
	return g.Wait()
}

// syncServers keeps the upstream manager matching the enabled servers in the database.
// A failed read is logged and retried: the gateway keeps serving what it already has.
func syncServers(ctx context.Context, st *store.Store, upstreams *upstream.Manager) error {
	ticker := time.NewTicker(serverPollInterval)
	defer ticker.Stop()
	for {
		servers, err := st.EnabledServers(ctx)
		if err != nil {
			slog.Error("reading servers", "err", err)
		} else {
			configs := make([]upstream.Config, len(servers))
			for i, s := range servers {
				configs[i] = upstream.Config{Slug: s.Slug, URL: s.URL, AuthType: upstream.AuthType(s.AuthType),
					AuthHeader: s.AuthHeader, CredentialEnc: s.CredentialEnc}
			}
			upstreams.Reconcile(ctx, configs)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// seedServers creates the upstreams listed in spec that do not exist yet. Each entry names
// the environment variable holding its credential, so the JSON itself carries no secrets.
func seedServers(ctx context.Context, st *store.Store, box *secretbox.Box, spec string) error {
	if spec == "" {
		return nil
	}
	var seeds []struct {
		store.ServerSeed
		CredentialEnv string `json:"credential_env"`
	}
	if err := json.Unmarshal([]byte(spec), &seeds); err != nil {
		return fmt.Errorf("SEED_SERVERS: %w", err)
	}
	for _, s := range seeds {
		if s.CredentialEnv != "" {
			s.Credential = os.Getenv(s.CredentialEnv)
			if s.Credential == "" {
				return fmt.Errorf("SEED_SERVERS: %s names credential variable %s, which is unset", s.Slug, s.CredentialEnv)
			}
		}
		inserted, err := st.SeedServer(ctx, box, s.ServerSeed)
		if err != nil {
			return fmt.Errorf("seeding server %s: %w", s.Slug, err)
		}
		if inserted {
			slog.Info("seeded server", "slug", s.Slug, "url", s.URL)
		}
	}
	return nil
}
