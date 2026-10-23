// Command gateway fronts every upstream MCP server behind one authenticated endpoint.
//
// Every request to /mcp must carry an API key (Authorization: Bearer gw_...).
//
// It reads DATABASE_URL and SECRETBOX_KEY (base64, 32 bytes), both required; LISTEN_ADDR,
// which defaults to ":8080"; and, to bootstrap an empty database:
//
//   - SEED_SERVERS, a JSON list of upstreams to create, each naming the environment variable
//     that holds its credential:
//     [{"slug": "woo", "url": "http://woo-mcp:8080/mcp", "auth_type": "bearer", "credential_env": "WOO_MCP_TOKEN"}]
//   - SEED_ADMIN_EMAIL and SEED_ADMIN_PASSWORD, the first admin, and optionally
//     SEED_ADMIN_API_KEY, a gw_ key for them generated elsewhere.
//
// The dashboard API is served under /api. Its session cookie is Secure unless
// INSECURE_COOKIES=true, which only a plain-HTTP local cluster should set. POST /api/chat
// runs an agent whose tool calls go through this gateway's own /mcp as the logged-in user.
// RATE_LIMIT_PER_MINUTE (default 120, 0 for none) caps each user's tool calls per minute,
// across replicas.
// With POD_IP and SESSION_KEY set, several replicas can serve /mcp behind any load balancer:
// each forwards requests for another replica's session to that replica.
// With PUBLIC_URL (the gateway's public base URL) and DASHBOARD_URL set, MCP clients can also
// sign in through a browser: the gateway is then an OAuth 2.1 authorization server, and /mcp
// accepts its access tokens beside API keys.
// Users create stores under /api/stores; STORE_DOMAIN (default shop.127.0.0.1.nip.io) is the
// domain their hosts sit under, and MAX_STORES_PER_USER (default 3) caps each user's stores.
// LLM_PROVIDER picks its model: groq (the default; GROQ_API_KEY, GROQ_MODEL) or gemini
// (GEMINI_API_KEY, GEMINI_MODEL). Without the chosen provider's key, chat answers 503.
//
// Seeding only creates what is missing, so operators' later changes survive restarts. The one
// exception is the seeded admin's password, which always follows SEED_ADMIN_PASSWORD.
package main

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/sync/errgroup"

	"github.com/kaizakin/mirage/internal/agent"
	"github.com/kaizakin/mirage/internal/agent/llm"
	"github.com/kaizakin/mirage/internal/api"
	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/gateway/affinity"
	"github.com/kaizakin/mirage/internal/gateway/downstream"
	"github.com/kaizakin/mirage/internal/gateway/naming"
	"github.com/kaizakin/mirage/internal/gateway/ratelimit"
	"github.com/kaizakin/mirage/internal/gateway/upstream"
	"github.com/kaizakin/mirage/internal/oauth"
	"github.com/kaizakin/mirage/internal/platform"
	"github.com/kaizakin/mirage/internal/secretbox"
	"github.com/kaizakin/mirage/internal/store"
)

const version = "0.1.0"

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

	db, err := store.OpenWhenReady(ctx, env["DATABASE_URL"])
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
	if err := seedAdmin(ctx, st); err != nil {
		return err
	}

	// The manager reports changes to the endpoint, which re-syncs its tools. Nothing connects
	// before syncServers first reconciles, so endpoint is set by the time OnChange runs.
	router, err := sessionRouter(os.Getenv("LISTEN_ADDR"))
	if err != nil {
		return err
	}
	var sessionID func() string
	if router != nil {
		sessionID = router.NewSessionID
	}

	var endpoint *downstream.Server
	upstreams := upstream.NewManager(box, upstream.Options{OnChange: func() { endpoint.Sync() }})
	defer upstreams.Close()
	limiter, err := rateLimiter(st)
	if err != nil {
		return err
	}
	endpoint = downstream.New(upstreams, downstream.Options{Version: version, Audit: st, SessionID: sessionID, Limiter: limiter})

	issuer, err := auth.NewTokenIssuer()
	if err != nil {
		return err
	}
	listen := cmp.Or(os.Getenv("LISTEN_ADDR"), ":8080")

	shops, err := shopOptions()
	if err != nil {
		return err
	}

	authServer, err := oauthServer(st)
	if err != nil {
		return err
	}

	// A buffered slot: requests coalesce while a reload is pending.
	reload := make(chan struct{}, 1)
	dashboard := api.New(api.Options{
		Chat:  chatAgent(issuer, listen),
		Store: st, Box: box, Upstreams: upstreams,
		Reload: func() {
			select {
			case reload <- struct{}{}:
			default:
			}
		},
		SecureCookie: os.Getenv("INSECURE_COOKIES") != "true",
		Shops:        shops,
		OAuth:        authServer,
	})

	mux := http.NewServeMux()
	mux.Handle("/api/", dashboard.Handler())
	// The middleware verifies the key on every request, so a revoked key stops working
	// inside an already open session.
	verifier := auth.VerifierOptions{Internal: &auth.Internal{Issuer: issuer, Users: st}}
	var bearer *sdkauth.RequireBearerTokenOptions
	if authServer != nil {
		authServer.Register(mux)
		verifier.OAuth = &auth.OAuth{Tokens: st, Resource: authServer.Resource()}
		// The 401 names the resource metadata, which is how a client finds where to sign in.
		bearer = &sdkauth.RequireBearerTokenOptions{ResourceMetadataURL: authServer.ResourceMetadataURL()}
	}
	mcpHandler := sdkauth.RequireBearerToken(auth.Verifier(st, verifier), bearer)(endpoint.Handler())
	if router != nil {
		// Outside authentication: a session's own replica checks the credential.
		mcpHandler = router.Handler(mcpHandler)
	}
	mux.Handle("/mcp", mcpHandler)
	platform.RegisterHealth(mux, db.PingContext)

	// Every job shares ctx: a signal, or any job failing, stops them all.
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return platform.Serve(ctx, listen, mux) })
	g.Go(func() error { return syncServers(ctx, st, upstreams, reload) })
	g.Go(func() error { return housekeeping(ctx, st) })
	return g.Wait()
}

// syncServers keeps the upstream manager matching the enabled servers in the database, on
// every tick and whenever the API asks after an edit. It is the only caller of Reconcile,
// whose upstream goroutines live under ctx; an API request's context would end them with
// the request. A failed read is logged and retried: the gateway keeps serving what it has.
func syncServers(ctx context.Context, st *store.Store, upstreams *upstream.Manager, reload <-chan struct{}) error {
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
		case <-reload:
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
		if err := naming.ValidateSlug(s.Slug); err != nil {
			return fmt.Errorf("SEED_SERVERS: %w", err)
		}
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

// seedAdmin creates the first admin from SEED_ADMIN_EMAIL and SEED_ADMIN_PASSWORD, plus an
// API key from SEED_ADMIN_API_KEY when set, unless they already exist.
func seedAdmin(ctx context.Context, st *store.Store) error {
	email, password, key := os.Getenv("SEED_ADMIN_EMAIL"), os.Getenv("SEED_ADMIN_PASSWORD"), os.Getenv("SEED_ADMIN_API_KEY")
	if email == "" {
		return nil
	}
	if password == "" {
		return errors.New("SEED_ADMIN_EMAIL is set but SEED_ADMIN_PASSWORD is not")
	}
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	var keyID string
	var keyHash []byte
	if key != "" {
		if keyID, keyHash, err = auth.HashKey(key); err != nil {
			return fmt.Errorf("SEED_ADMIN_API_KEY: %w", err)
		}
	}
	userCreated, keyCreated, err := st.SeedAdmin(ctx, email, string(passwordHash), keyID, keyHash)
	if err != nil {
		return fmt.Errorf("seeding admin: %w", err)
	}
	if !userCreated {
		if err := syncAdminPassword(ctx, st, email, password, string(passwordHash)); err != nil {
			return err
		}
	}
	if userCreated || keyCreated {
		slog.Info("seeded admin", "email", email, "user_created", userCreated, "key_created", keyCreated, "key_id", keyID)
	}
	return nil
}

// syncAdminPassword makes an existing seeded admin's password match SEED_ADMIN_PASSWORD, so
// the Secret stays the one place that password is set.
func syncAdminPassword(ctx context.Context, st *store.Store, email, password, passwordHash string) error {
	admin, current, err := st.UserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("seeding admin: %w", err)
	}
	if bcrypt.CompareHashAndPassword([]byte(current), []byte(password)) == nil {
		return nil
	}
	if err := st.SetPassword(ctx, admin.ID, passwordHash); err != nil {
		return fmt.Errorf("seeding admin password: %w", err)
	}
	slog.Info("reset admin password to SEED_ADMIN_PASSWORD", "email", email)
	return nil
}

// sessionRouter returns the router that lets several replicas share /mcp sessions, or nil
// for a single replica: it needs POD_IP, where other replicas reach this one, and
// SESSION_KEY (base64, at least 16 bytes), which all replicas share.
func sessionRouter(listen string) (*affinity.Router, error) {
	podIP := os.Getenv("POD_IP")
	if podIP == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(os.Getenv("SESSION_KEY"))
	if err != nil {
		return nil, fmt.Errorf("SESSION_KEY is not base64: %w", err)
	}
	_, port, err := net.SplitHostPort(cmp.Or(listen, ":8080"))
	if err != nil {
		return nil, err
	}
	return affinity.New(net.JoinHostPort(podIP, port), key)
}

// oauthServer returns the authorization server for browser sign-in, or nil when PUBLIC_URL
// is unset: an issuer has to know its own public address.
func oauthServer(st *store.Store) (*oauth.Server, error) {
	public := os.Getenv("PUBLIC_URL")
	if public == "" {
		return nil, nil
	}
	dashboard := os.Getenv("DASHBOARD_URL")
	if dashboard == "" {
		return nil, errors.New("PUBLIC_URL is set but DASHBOARD_URL, where users approve sign-ins, is not")
	}
	return oauth.New(st, oauth.Config{Issuer: public, AuthorizeURL: strings.TrimSuffix(dashboard, "/") + "/oauth/authorize"}), nil
}

// rateLimiter returns the per-user tool call limit, RATE_LIMIT_PER_MINUTE (default 120), or
// nil when it is 0.
func rateLimiter(st *store.Store) (downstream.Limiter, error) {
	perMinute := 120
	if v := os.Getenv("RATE_LIMIT_PER_MINUTE"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("RATE_LIMIT_PER_MINUTE must be a whole number, got %q", v)
		}
		perMinute = n
	}
	if perMinute == 0 {
		return nil, nil
	}
	return ratelimit.New(st, perMinute), nil
}

// housekeeping clears used-up OAuth records and old rate limit counts every hour. Any
// replica may do it; the deletes are idempotent.
func housekeeping(ctx context.Context, st *store.Store) error {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if err := errors.Join(st.PruneOAuth(ctx), st.PruneRateLimits(ctx)); err != nil && ctx.Err() == nil {
			slog.Error("housekeeping", "err", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// shopOptions reads where provisioned stores are served and how many each user may have.
func shopOptions() (api.ShopOptions, error) {
	opts := api.ShopOptions{Domain: cmp.Or(os.Getenv("STORE_DOMAIN"), "shop.127.0.0.1.nip.io"), MaxPerUser: 3}
	if v := os.Getenv("MAX_STORES_PER_USER"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return opts, fmt.Errorf("MAX_STORES_PER_USER must be a positive integer, got %q", v)
		}
		opts.MaxPerUser = n
	}
	return opts, nil
}

// chatAgent returns the /api/chat handler, or a handler explaining why chat is off. The
// agent reaches this process's /mcp over loopback.
func chatAgent(issuer *auth.TokenIssuer, listen string) http.Handler {
	provider, err := chatProvider(cmp.Or(os.Getenv("LLM_PROVIDER"), "groq"))
	if err != nil {
		slog.Warn("chat is off", "reason", err)
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "chat is not configured: "+err.Error(), http.StatusServiceUnavailable)
		})
	}
	_, port, err := net.SplitHostPort(listen)
	if err != nil {
		port = "8080"
	}
	return agent.New(provider, issuer, "http://127.0.0.1:"+port+"/mcp")
}

// chatProvider builds the named model provider from its key and model variables.
func chatProvider(name string) (llm.Provider, error) {
	switch name {
	case "groq":
		key := os.Getenv("GROQ_API_KEY")
		if key == "" {
			return nil, errors.New("LLM_PROVIDER is groq but GROQ_API_KEY is unset")
		}
		return &llm.Groq{APIKey: key, Model: cmp.Or(os.Getenv("GROQ_MODEL"), "openai/gpt-oss-120b")}, nil
	case "gemini":
		key := os.Getenv("GEMINI_API_KEY")
		if key == "" {
			return nil, errors.New("LLM_PROVIDER is gemini but GEMINI_API_KEY is unset")
		}
		return &llm.Gemini{APIKey: key, Model: cmp.Or(os.Getenv("GEMINI_MODEL"), "gemini-3.5-flash")}, nil
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER %q; use groq or gemini", name)
	}
}
