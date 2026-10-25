// Package provisioner turns rows in the shops table into running stores. It is level
// triggered: every pass compares each store's desired state, in Postgres, with what exists in
// the cluster, and takes the next step. Nothing it needs lives only in its memory, so after a
// restart the next pass carries on where the last one stopped, and a store either completes
// or fails with a reason before its deadline.
package provisioner

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"sync"
	"time"

	"github.com/kaizakin/mirage/internal/secretbox"
	"github.com/kaizakin/mirage/internal/store"
)

// Shops is the part of the store database the provisioner works from.
type Shops interface {
	Shops(ctx context.Context, ownerID int64) ([]store.Shop, error)
	SetShopSecrets(ctx context.Context, id int64, sealed []byte) ([]byte, error)
	MarkShopReady(ctx context.Context, box *secretbox.Box, id int64, srv store.ShopServer) error
	MarkShopFailed(ctx context.Context, id int64, reason string) error
	RegisterShopServer(ctx context.Context, box *secretbox.Box, id int64, srv store.ShopServer) error
	UnregisterShopServer(ctx context.Context, id int64) error
	DeleteShop(ctx context.Context, id int64) error
}

// ReleaseState is what Helm knows about a store's release.
type ReleaseState int

// Release states.
const (
	// ReleaseAbsent: never installed, or uninstalled.
	ReleaseAbsent ReleaseState = iota
	// ReleaseDeployed: installed; its resources may still be starting.
	ReleaseDeployed
	// ReleaseStuck: an install that did not finish (the provisioner stopped mid-operation,
	// or Helm failed). It is uninstalled and installed again.
	ReleaseStuck
)

// Health is how far a deployed store has come.
type Health struct {
	// Ready: setup finished and the MCP server is serving.
	Ready bool
	// Broken is why the store can never become ready, such as its setup Job having failed;
	// empty while it may still get there.
	Broken string
	// Waiting says what it is waiting for, for the timeout's reason.
	Waiting string
}

// Cluster is what the provisioner does to Kubernetes. All of it is idempotent.
type Cluster interface {
	EnsureNamespace(ctx context.Context, namespace, slug string) error
	// DeleteNamespace starts deleting the namespace and reports whether it is gone.
	DeleteNamespace(ctx context.Context, namespace string) (gone bool, err error)
	Release(ctx context.Context, namespace string) (ReleaseState, error)
	// Install installs the store chart without waiting for its resources.
	Install(ctx context.Context, namespace string, values map[string]any) error
	Uninstall(ctx context.Context, namespace string) error
	Health(ctx context.Context, namespace string) (Health, error)
}

// Config tunes a Provisioner.
type Config struct {
	// Timeout bounds one provisioning attempt, from when it was asked for.
	Timeout time.Duration
	// GatewayNamespace is where the gateway runs; only it may call a store's MCP server.
	GatewayNamespace string
	// Interval between passes; default 3s.
	Interval time.Duration
	// Parallel caps the stores worked on at once; default 4.
	Parallel int
	// Scheme of the stores' public URLs, http or https; default http.
	Scheme string
	// StoreValues are chart values every store is installed with, such as TLS or a resource
	// profile. The values the provisioner sets per store take precedence.
	StoreValues map[string]any
}

// Provisioner reconciles stores.
type Provisioner struct {
	shops   Shops
	cluster Cluster
	box     *secretbox.Box
	cfg     Config
	now     func() time.Time

	mu      sync.Mutex
	busy    map[int64]bool   // stores with a step in flight
	lastErr map[int64]string // each store's error from its last step, for a timeout's reason
}

// New returns a Provisioner.
func New(shops Shops, cluster Cluster, box *secretbox.Box, cfg Config) *Provisioner {
	if cfg.Interval <= 0 {
		cfg.Interval = 3 * time.Second
	}
	if cfg.Parallel <= 0 {
		cfg.Parallel = 4
	}
	if cfg.Scheme == "" {
		cfg.Scheme = "http"
	}
	return &Provisioner{shops: shops, cluster: cluster, box: box, cfg: cfg, now: time.Now,
		busy: map[int64]bool{}, lastErr: map[int64]string{}}
}

// Run reconciles every store on every interval until ctx ends. Stores are worked on in
// parallel, but each by at most one step at a time.
func (p *Provisioner) Run(ctx context.Context) error {
	slots := make(chan struct{}, p.cfg.Parallel)
	var wg sync.WaitGroup
	defer wg.Wait()
	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	for {
		shops, err := p.shops.Shops(ctx, 0)
		if err != nil {
			slog.Error("listing stores", "err", err)
		}
		for _, shop := range shops {
			if !p.claim(shop.ID) {
				continue
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				p.release(shop.ID, nil)
				return nil
			}
			wg.Go(func() {
				defer func() { <-slots }()
				stepCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
				defer cancel()
				err := p.Step(stepCtx, shop)
				if err != nil && ctx.Err() == nil {
					slog.Warn("store step failed; retrying", "store", shop.Slug, "status", shop.Status, "err", err)
				}
				p.release(shop.ID, err)
			})
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (p *Provisioner) claim(id int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.busy[id] {
		return false
	}
	p.busy[id] = true
	return true
}

func (p *Provisioner) release(id int64, err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.busy, id)
	if err != nil {
		p.lastErr[id] = err.Error()
	} else {
		delete(p.lastErr, id)
	}
}

// Step takes the next step toward the store's desired state. An error leaves the store as
// it was, for the next pass to try again.
func (p *Provisioner) Step(ctx context.Context, shop store.Shop) error {
	switch shop.Status {
	case store.ShopProvisioning:
		return p.provision(ctx, shop)
	case store.ShopReady:
		// Puts back a registration that went missing, such as one an operator deleted.
		secrets, err := p.secrets(ctx, shop)
		if err != nil {
			return err
		}
		return p.shops.RegisterShopServer(ctx, p.box, shop.ID, p.server(shop, secrets))
	case store.ShopFailed:
		// A failed store keeps its row and reason but frees its resources.
		_, err := p.cluster.DeleteNamespace(ctx, shop.Namespace())
		return err
	case store.ShopDeleting:
		return p.teardown(ctx, shop)
	}
	return fmt.Errorf("store %s has unknown status %q", shop.Slug, shop.Status)
}

func (p *Provisioner) provision(ctx context.Context, shop store.Shop) error {
	if p.now().Sub(shop.ProvisioningSince) > p.cfg.Timeout {
		return p.fail(ctx, shop, fmt.Sprintf("not ready after %s: %s", p.cfg.Timeout, p.stuckReason(ctx, shop)))
	}
	secrets, err := p.secrets(ctx, shop)
	if err != nil {
		return err
	}
	ns := shop.Namespace()
	if err := p.cluster.EnsureNamespace(ctx, ns, shop.Slug); err != nil {
		return err
	}
	state, err := p.cluster.Release(ctx, ns)
	if err != nil {
		return err
	}
	switch state {
	case ReleaseAbsent:
		return p.cluster.Install(ctx, ns, p.values(shop, secrets))
	case ReleaseStuck:
		// Every value comes from the store's row, so the reinstall matches what the store's
		// volumes already hold.
		slog.Info("reinstalling a release left unfinished", "store", shop.Slug)
		return p.cluster.Uninstall(ctx, ns)
	}
	health, err := p.cluster.Health(ctx, ns)
	switch {
	case err != nil:
		return err
	case health.Broken != "":
		return p.fail(ctx, shop, health.Broken)
	case !health.Ready:
		return nil
	}
	err = p.shops.MarkShopReady(ctx, p.box, shop.ID, p.server(shop, secrets))
	if errors.Is(err, store.ErrNotFound) {
		return nil // deleted meanwhile; the next pass tears it down
	}
	if err == nil {
		slog.Info("store ready", "store", shop.Slug, "took", p.now().Sub(shop.ProvisioningSince).Round(time.Second))
	}
	return err
}

// stuckReason explains why a store is not ready yet: the last step error if steps were
// failing, otherwise what the store is waiting for.
func (p *Provisioner) stuckReason(ctx context.Context, shop store.Shop) string {
	p.mu.Lock()
	last := p.lastErr[shop.ID]
	p.mu.Unlock()
	if last != "" {
		return last
	}
	health, err := p.cluster.Health(ctx, shop.Namespace())
	switch {
	case err != nil:
		return err.Error()
	case health.Waiting != "":
		return health.Waiting
	}
	return "its resources were never created"
}

func (p *Provisioner) fail(ctx context.Context, shop store.Shop, reason string) error {
	err := p.shops.MarkShopFailed(ctx, shop.ID, reason)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	slog.Warn("store failed", "store", shop.Slug, "reason", reason)
	_, err = p.cluster.DeleteNamespace(ctx, shop.Namespace())
	return err
}

func (p *Provisioner) teardown(ctx context.Context, shop store.Shop) error {
	// The tools go first, so no client calls a store that is being removed.
	if err := p.shops.UnregisterShopServer(ctx, shop.ID); err != nil {
		return err
	}
	gone, err := p.cluster.DeleteNamespace(ctx, shop.Namespace())
	if err != nil || !gone {
		return err
	}
	if err := p.shops.DeleteShop(ctx, shop.ID); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	p.mu.Lock()
	delete(p.lastErr, shop.ID)
	p.mu.Unlock()
	slog.Info("store deleted", "store", shop.Slug)
	return nil
}

// shopSecrets are a store's generated credentials. They are made once and kept, sealed, in
// its row.
type shopSecrets struct {
	MCPToken          string `json:"mcp_token"`
	MySQLPassword     string `json:"mysql_password"`
	MySQLRootPassword string `json:"mysql_root_password"`
	AdminPassword     string `json:"admin_password"`
}

func (p *Provisioner) secrets(ctx context.Context, shop store.Shop) (*shopSecrets, error) {
	sealed := shop.SecretsEnc
	if sealed == nil {
		fresh := shopSecrets{MCPToken: randomToken(), MySQLPassword: randomToken(), MySQLRootPassword: randomToken(), AdminPassword: randomToken()}
		plain, err := json.Marshal(fresh)
		if err != nil {
			return nil, err
		}
		if sealed, err = p.box.Encrypt(plain, secretsAD(shop)); err != nil {
			return nil, err
		}
		// Whichever writer stored secrets first wins; use those.
		if sealed, err = p.shops.SetShopSecrets(ctx, shop.ID, sealed); err != nil {
			return nil, err
		}
	}
	plain, err := p.box.Decrypt(sealed, secretsAD(shop))
	if err != nil {
		return nil, fmt.Errorf("opening the store's secrets: %w", err)
	}
	var s shopSecrets
	return &s, json.Unmarshal(plain, &s)
}

func secretsAD(shop store.Shop) []byte { return []byte("shop-secrets:" + shop.Slug) }

func randomToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b) // crypto/rand.Read never returns an error
	return base64.RawURLEncoding.EncodeToString(b)
}

// releaseName is the Helm release name in every store namespace; the chart's resources are
// named after it.
const releaseName = "store"

// server is the store's registration in the gateway.
func (p *Provisioner) server(shop store.Shop, secrets *shopSecrets) store.ShopServer {
	return store.ShopServer{
		Slug:       shop.ServerSlug(),
		URL:        fmt.Sprintf("http://%s-woo-mcp.%s.svc.cluster.local:8080/mcp", releaseName, shop.Namespace()),
		Credential: secrets.MCPToken,
	}
}

func (p *Provisioner) values(shop store.Shop, s *shopSecrets) map[string]any {
	return mergeValues(p.cfg.StoreValues, map[string]any{
		"gatewayNamespace": p.cfg.GatewayNamespace,
		"shop": map[string]any{
			"setup": map[string]any{
				"siteUrl":       p.cfg.Scheme + "://" + shop.Host,
				"title":         shop.Name,
				"adminPassword": s.AdminPassword,
			},
			"wordpress": map[string]any{"ingress": map[string]any{"host": shop.Host}},
			"mysql":     map[string]any{"password": s.MySQLPassword, "rootPassword": s.MySQLRootPassword},
		},
		"woo-mcp": map[string]any{"bearerToken": s.MCPToken, "gatewayNamespace": p.cfg.GatewayNamespace},
	})
}

// mergeValues returns base with over laid on top, merging nested maps key by key. Neither
// argument is modified.
func mergeValues(base, over map[string]any) map[string]any {
	out := maps.Clone(base)
	if out == nil {
		out = map[string]any{}
	}
	for k, v := range over {
		if overMap, ok := v.(map[string]any); ok {
			if baseMap, ok := out[k].(map[string]any); ok {
				v = mergeValues(baseMap, overMap)
			}
		}
		out[k] = v
	}
	return out
}
