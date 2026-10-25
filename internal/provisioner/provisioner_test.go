package provisioner

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kaizakin/mirage/internal/secretbox"
	"github.com/kaizakin/mirage/internal/store"
)

// fakeShops keeps rows in memory and applies the same status guards as the real store.
type fakeShops struct {
	mu      sync.Mutex
	rows    map[int64]*store.Shop
	servers map[int64]store.ShopServer
}

func newFakeShops(shops ...store.Shop) *fakeShops {
	f := &fakeShops{rows: map[int64]*store.Shop{}, servers: map[int64]store.ShopServer{}}
	for _, s := range shops {
		f.rows[s.ID] = &s
	}
	return f
}

func (f *fakeShops) get(id int64) store.Shop {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.rows[id]; ok {
		return *s
	}
	return store.Shop{}
}

func (f *fakeShops) Shops(context.Context, int64) ([]store.Shop, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []store.Shop
	for _, s := range f.rows {
		out = append(out, *s)
	}
	return out, nil
}

func (f *fakeShops) SetShopSecrets(_ context.Context, id int64, sealed []byte) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows[id].SecretsEnc == nil {
		f.rows[id].SecretsEnc = sealed
	}
	return f.rows[id].SecretsEnc, nil
}

func (f *fakeShops) transition(id int64, from, to store.ShopStatus) error {
	if s, ok := f.rows[id]; !ok || s.Status != from {
		return store.ErrNotFound
	}
	f.rows[id].Status = to
	return nil
}

func (f *fakeShops) MarkShopReady(_ context.Context, _ *secretbox.Box, id int64, srv store.ShopServer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.transition(id, store.ShopProvisioning, store.ShopReady); err != nil {
		return err
	}
	f.servers[id] = srv
	return nil
}

func (f *fakeShops) MarkShopFailed(_ context.Context, id int64, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.transition(id, store.ShopProvisioning, store.ShopFailed); err != nil {
		return err
	}
	f.rows[id].Reason = reason
	return nil
}

func (f *fakeShops) RegisterShopServer(_ context.Context, _ *secretbox.Box, id int64, srv store.ShopServer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.servers[id]; !ok {
		f.servers[id] = srv
	}
	return nil
}

func (f *fakeShops) UnregisterShopServer(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.servers, id)
	return nil
}

func (f *fakeShops) DeleteShop(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.rows[id] == nil || f.rows[id].Status != store.ShopDeleting {
		return store.ErrNotFound
	}
	delete(f.rows, id)
	return nil
}

// fakeCluster is one namespace per store, with a release and a health the test sets.
type fakeCluster struct {
	mu         sync.Mutex
	namespaces map[string]bool
	releases   map[string]ReleaseState
	health     map[string]Health
	installed  map[string]map[string]any // the values of each namespace's last install
	installs   int
	onInstall  func() // runs inside Install, outside the lock
	keepAlive  bool   // namespaces take another pass to disappear
}

func newFakeCluster() *fakeCluster {
	return &fakeCluster{namespaces: map[string]bool{}, releases: map[string]ReleaseState{},
		health: map[string]Health{}, installed: map[string]map[string]any{}}
}

func (c *fakeCluster) EnsureNamespace(_ context.Context, ns, _ string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.namespaces[ns] = true
	return nil
}

func (c *fakeCluster) DeleteNamespace(_ context.Context, ns string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.keepAlive && c.namespaces[ns] {
		c.keepAlive = false
		return false, nil
	}
	delete(c.namespaces, ns)
	delete(c.releases, ns)
	return true, nil
}

func (c *fakeCluster) Release(_ context.Context, ns string) (ReleaseState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.releases[ns], nil
}

func (c *fakeCluster) Install(_ context.Context, ns string, values map[string]any) error {
	if c.onInstall != nil {
		c.onInstall()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releases[ns] = ReleaseDeployed
	c.installed[ns] = values
	c.installs++
	return nil
}

func (c *fakeCluster) Uninstall(_ context.Context, ns string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releases[ns] = ReleaseAbsent
	return nil
}

func (c *fakeCluster) Health(_ context.Context, ns string) (Health, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.health[ns], nil
}

func (c *fakeCluster) set(ns string, h Health) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.health[ns] = h
}

func testBox(t *testing.T) *secretbox.Box {
	t.Helper()
	box, err := secretbox.New(make([]byte, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return box
}

func newShop(id int64, slug string) store.Shop {
	return store.Shop{ID: id, Slug: slug, Name: "Tea House", Host: slug + ".shop.test", Status: store.ShopProvisioning, ProvisioningSince: time.Now()}
}

func setup(t *testing.T, shops ...store.Shop) (*Provisioner, *fakeShops, *fakeCluster) {
	t.Helper()
	db, cluster := newFakeShops(shops...), newFakeCluster()
	return New(db, cluster, testBox(t), Config{Timeout: 10 * time.Minute, GatewayNamespace: "mirage"}), db, cluster
}

func step(t *testing.T, p *Provisioner, db *fakeShops, id int64) {
	t.Helper()
	if err := p.Step(t.Context(), db.get(id)); err != nil {
		t.Fatal(err)
	}
}

func valueAt(values map[string]any, path ...string) any {
	var v any = values
	for _, k := range path {
		v = v.(map[string]any)[k]
	}
	return v
}

func TestStoreIsInstalledThenRegisteredWhenHealthy(t *testing.T) {
	p, db, cluster := setup(t, newShop(1, "tea"))

	step(t, p, db, 1)
	values := cluster.installed["store-tea"]
	if values == nil || !cluster.namespaces["store-tea"] {
		t.Fatal("the first step must create the namespace and install the chart")
	}
	if valueAt(values, "shop", "setup", "siteUrl") != "http://tea.shop.test" || valueAt(values, "shop", "setup", "title") != "Tea House" ||
		valueAt(values, "woo-mcp", "gatewayNamespace") != "mirage" {
		t.Fatalf("install values = %v", values)
	}

	cluster.set("store-tea", Health{Waiting: "setup is running"})
	step(t, p, db, 1)
	if got := db.get(1).Status; got != store.ShopProvisioning {
		t.Fatalf("status while waiting = %s", got)
	}

	cluster.set("store-tea", Health{Ready: true})
	step(t, p, db, 1)
	srv := db.servers[1]
	if db.get(1).Status != store.ShopReady || srv.Slug != "store-tea" ||
		srv.URL != "http://store-woo-mcp.store-tea.svc.cluster.local:8080/mcp" || srv.Credential != valueAt(values, "woo-mcp", "bearerToken") {
		t.Fatalf("after healthy: status %s, server %+v", db.get(1).Status, srv)
	}
	if cluster.installs != 1 {
		t.Fatalf("installed %d times, want 1", cluster.installs)
	}
}

func TestStoreValuesUnderlieEachStoresOwn(t *testing.T) {
	db, cluster := newFakeShops(newShop(1, "tea"), newShop(2, "mugs")), newFakeCluster()
	shared := map[string]any{
		"quota": map[string]any{"requests.memory": "1Gi"},
		"shop": map[string]any{
			"setup":     map[string]any{"title": "Shared", "currency": "USD"},
			"wordpress": map[string]any{"ingress": map[string]any{"tls": map[string]any{"enabled": true, "clusterIssuer": "letsencrypt"}}},
		},
	}
	p := New(db, cluster, testBox(t), Config{Timeout: 10 * time.Minute, GatewayNamespace: "mirage", Scheme: "https", StoreValues: shared})

	step(t, p, db, 1)
	step(t, p, db, 2)
	for ns, host := range map[string]string{"store-tea": "tea.shop.test", "store-mugs": "mugs.shop.test"} {
		values := cluster.installed[ns]
		checks := map[string][2]any{
			"siteUrl":  {valueAt(values, "shop", "setup", "siteUrl"), "https://" + host},
			"host":     {valueAt(values, "shop", "wordpress", "ingress", "host"), host},
			"issuer":   {valueAt(values, "shop", "wordpress", "ingress", "tls", "clusterIssuer"), "letsencrypt"},
			"currency": {valueAt(values, "shop", "setup", "currency"), "USD"},
			"quota":    {valueAt(values, "quota", "requests.memory"), "1Gi"},
		}
		for name, c := range checks {
			if c[0] != c[1] {
				t.Errorf("%s: %s = %v, want %v", ns, name, c[0], c[1])
			}
		}
		if valueAt(values, "shop", "setup", "title") == "Shared" {
			t.Errorf("%s: the store's own title must win over the shared one", ns)
		}
	}
	if _, leaked := valueAt(shared, "shop", "wordpress", "ingress").(map[string]any)["host"]; leaked {
		t.Error("installing a store modified the shared values")
	}
}

func TestReleaseLeftUnfinishedIsReinstalledWithTheSameSecrets(t *testing.T) {
	p, db, cluster := setup(t, newShop(1, "tea"))
	step(t, p, db, 1)
	first := cluster.installed["store-tea"]

	// The provisioner stopped mid-install; a new one finds the release stuck.
	cluster.releases["store-tea"] = ReleaseStuck
	restarted := New(db, cluster, testBox(t), Config{Timeout: 10 * time.Minute, GatewayNamespace: "mirage"})
	step(t, restarted, db, 1)
	if cluster.releases["store-tea"] != ReleaseAbsent {
		t.Fatal("a stuck release must be uninstalled")
	}
	step(t, restarted, db, 1)
	second := cluster.installed["store-tea"]
	for _, path := range [][]string{{"shop", "mysql", "password"}, {"shop", "mysql", "rootPassword"}, {"woo-mcp", "bearerToken"}, {"shop", "setup", "adminPassword"}} {
		if valueAt(first, path...) != valueAt(second, path...) {
			t.Fatalf("%v changed across the reinstall; the store's volumes hold the old one", path)
		}
	}
	cluster.set("store-tea", Health{Ready: true})
	step(t, restarted, db, 1)
	if db.get(1).Status != store.ShopReady {
		t.Fatalf("status = %s, want the store to complete after the restart", db.get(1).Status)
	}
}

func TestProvisioningPastItsTimeoutFailsWithTheReason(t *testing.T) {
	shop := newShop(1, "tea")
	p, db, cluster := setup(t, shop)
	step(t, p, db, 1)
	cluster.set("store-tea", Health{Waiting: "pod store-mysql-0 cannot be scheduled: exceeded quota"})

	p.now = func() time.Time { return shop.ProvisioningSince.Add(11 * time.Minute) }
	step(t, p, db, 1)
	got := db.get(1)
	if got.Status != store.ShopFailed || !strings.Contains(got.Reason, "not ready after 10m0s") || !strings.Contains(got.Reason, "exceeded quota") {
		t.Fatalf("after the deadline: status %s, reason %q", got.Status, got.Reason)
	}
	if cluster.namespaces["store-tea"] {
		t.Fatal("a failed store's namespace must be deleted, so it holds no resources")
	}
}

func TestBrokenStoreFailsAtOnce(t *testing.T) {
	p, db, cluster := setup(t, newShop(1, "tea"))
	step(t, p, db, 1)
	cluster.set("store-tea", Health{Broken: "store setup failed: BackoffLimitExceeded"})
	step(t, p, db, 1)
	if got := db.get(1); got.Status != store.ShopFailed || got.Reason != "store setup failed: BackoffLimitExceeded" {
		t.Fatalf("status %s, reason %q", got.Status, got.Reason)
	}
}

func TestDeletingUnregistersFirstAndRemovesTheRowOnlyOnceTheNamespaceIsGone(t *testing.T) {
	p, db, cluster := setup(t, newShop(1, "tea"))
	step(t, p, db, 1)
	cluster.set("store-tea", Health{Ready: true})
	step(t, p, db, 1)

	db.rows[1].Status = store.ShopDeleting
	cluster.keepAlive = true
	step(t, p, db, 1)
	if _, ok := db.servers[1]; ok {
		t.Fatal("the store's MCP server must be unregistered before its namespace goes")
	}
	if db.get(1).ID == 0 {
		t.Fatal("the row must stay while the namespace is still terminating")
	}
	step(t, p, db, 1)
	if db.get(1).ID != 0 || cluster.namespaces["store-tea"] {
		t.Fatal("once the namespace is gone the row must be deleted")
	}
}

func TestReadyStoreGetsAMissingRegistrationBack(t *testing.T) {
	p, db, cluster := setup(t, newShop(1, "tea"))
	step(t, p, db, 1)
	cluster.set("store-tea", Health{Ready: true})
	step(t, p, db, 1)
	token := db.servers[1].Credential

	delete(db.servers, 1)
	step(t, p, db, 1)
	if db.servers[1].Credential != token || token == "" {
		t.Fatalf("re-registered server %+v; want the store's original token", db.servers[1])
	}
}

func TestStoresProvisionInParallel(t *testing.T) {
	p, _, cluster := setup(t, newShop(1, "tea"), newShop(2, "coffee"))
	p.cfg.Interval = 10 * time.Millisecond
	// Each install waits until both have started; one at a time would never get past it.
	var started sync.WaitGroup
	started.Add(2)
	cluster.onInstall = func() {
		started.Done()
		started.Wait()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() { _ = p.Run(ctx) }()
	for ctx.Err() == nil {
		cluster.mu.Lock()
		n := cluster.installs
		cluster.mu.Unlock()
		if n == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the two stores were not installed concurrently")
}
