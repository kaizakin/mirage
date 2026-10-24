package store

import (
	"io/fs"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/kaizakin/mirage/internal/secretbox"
)

func internalBySlug(t *testing.T, s *Store) map[string]bool {
	t.Helper()
	rows, err := s.Servers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.Slug] = r.Internal
	}
	enabled, err := s.EnabledServers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range enabled {
		if e.Internal != out[e.Slug] {
			t.Fatalf("%s: EnabledServers says internal=%v, Servers says %v", e.Slug, e.Internal, out[e.Slug])
		}
	}
	return out
}

func TestLiveOnlyThePlatformMarksServersInternal(t *testing.T) {
	s := migrated(t)
	box, _ := secretbox.New(make([]byte, secretbox.KeySize))
	ctx := t.Context()
	currency := ServerSeed{Slug: "currency", URL: "http://mirage-currency-mcp:8080/mcp", AuthType: "none"}

	if _, err := s.SeedServer(ctx, box, currency); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateServer(ctx, box, ServerSeed{Slug: "deepwiki", URL: "https://mcp.deepwiki.com/mcp", AuthType: "none"}); err != nil {
		t.Fatal(err)
	}
	ada, _ := s.CreateUser(ctx, "ada@example.com", "h", "member")
	shop, _, err := s.CreateShop(ctx, NewShop{Slug: "tea", Name: "Tea", Host: "tea.example", OwnerID: ada.ID, MaxPerOwner: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkShopReady(ctx, box, shop.ID, ShopServer{Slug: "store-tea", URL: "http://store-woo-mcp.store-tea.svc.cluster.local:8080/mcp", Credential: "t"}); err != nil {
		t.Fatal(err)
	}
	if got := internalBySlug(t, s); !got["currency"] || got["deepwiki"] || !got["store-tea"] {
		t.Fatalf("internal = %v, want currency and store-tea only", got)
	}

	// A row seeded before the flag existed regains it on the next start.
	mustExec(t, s.db, `UPDATE servers SET internal = false WHERE slug = 'currency'`)
	if inserted, err := s.SeedServer(ctx, box, currency); err != nil || inserted {
		t.Fatalf("re-seed: inserted=%v err=%v", inserted, err)
	}
	if !internalBySlug(t, s)["currency"] {
		t.Fatal("re-seeding did not mark the seeded server internal")
	}

	disabled := false
	if err := s.UpdateServer(ctx, box, "currency", ServerPatch{Enabled: &disabled}); err != nil {
		t.Fatal(err)
	}
	if !internalBySlug(t, s)["currency"] {
		t.Fatal("disabling the server dropped its trust")
	}
	moved := "https://evil.example/mcp"
	if err := s.UpdateServer(ctx, box, "currency", ServerPatch{URL: &moved}); err != nil {
		t.Fatal(err)
	}
	if internalBySlug(t, s)["currency"] {
		t.Fatal("an admin-chosen URL kept the seeded server's trust")
	}
	if _, err := s.SeedServer(ctx, box, currency); err != nil {
		t.Fatal(err)
	}
	if internalBySlug(t, s)["currency"] {
		t.Fatal("re-seeding trusted a URL the operator did not configure")
	}
}

func TestLiveServerTrustMigrationMarksStoreServers(t *testing.T) {
	db := freshDB(t)
	migrations, err := fs.Sub(embedded, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 6); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `INSERT INTO shops (slug, name, host) VALUES ('tea', 'Tea', 'tea.example')`)
	mustExec(t, db, `INSERT INTO servers (slug, url, auth_type, shop_id) VALUES ('store-tea', 'http://woo/mcp', 'none', (SELECT id FROM shops))`)
	mustExec(t, db, `INSERT INTO servers (slug, url, auth_type) VALUES ('deepwiki', 'https://mcp.deepwiki.com/mcp', 'none')`)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	if got := internalBySlug(t, New(db)); !got["store-tea"] || got["deepwiki"] {
		t.Fatalf("internal after migration = %v, want store-tea only", got)
	}
}
