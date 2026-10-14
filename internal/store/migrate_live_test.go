package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// TestLiveMigrations runs against a real Postgres, e.g. through
//
//	kubectl -n mirage port-forward svc/mirage-postgres 15432:5432
//
// with STORE_TEST_DSN set to a URL whose user may create databases. It migrates a
// throwaway database, never the one in the DSN.
func TestLiveMigrations(t *testing.T) {
	dsn := os.Getenv("STORE_TEST_DSN")
	if dsn == "" {
		t.Skip("STORE_TEST_DSN not set; skipping live migration test")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	admin, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Registered before the drop below; cleanups run last-in-first-out, so this runs after it.
	t.Cleanup(func() { _ = admin.Close() })
	name := "migrate_test_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Cleanup runs after t.Context is canceled.
		if _, err := admin.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)"); err != nil {
			t.Errorf("drop %s: %v", name, err)
		}
	})

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	db, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	// Two replicas starting together: the advisory lock must let both succeed.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { errs[i] = Migrate(ctx, db) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Migrate %d: %v", i, err)
		}
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatalf("Migrate on an up-to-date database: %v", err)
	}

	tables := queryStrings(t, db, `SELECT table_name FROM information_schema.tables WHERE table_schema = 'public' ORDER BY 1`)
	for _, want := range []string{"api_keys", "audit_log", "invites", "servers", "users"} {
		if !slices.Contains(tables, want) {
			t.Errorf("table %s missing; have %v", want, tables)
		}
	}

	mustExec(t, db, `INSERT INTO users (email, password_hash, role) VALUES ('Ada@example.com', 'h', 'admin')`)
	mustReject(t, db, "duplicate email in another case", `INSERT INTO users (email, password_hash, role) VALUES ('ada@example.com', 'h', 'member')`)
	mustReject(t, db, "unknown role", `INSERT INTO users (email, password_hash, role) VALUES ('b@example.com', 'h', 'owner')`)

	mustExec(t, db, `INSERT INTO servers (slug, url) VALUES ('deepwiki', 'https://mcp.deepwiki.com/mcp')`)
	mustExec(t, db, `INSERT INTO servers (slug, url, auth_type, credential_enc) VALUES ('woo', 'http://woo:8080/mcp', 'bearer', '\x01')`)
	mustExec(t, db, `INSERT INTO servers (slug, url, auth_type, auth_header, credential_enc) VALUES ('currency', 'http://cur:8080/mcp', 'header', 'X-Api-Key', '\x01')`)
	mustReject(t, db, "duplicate slug", `INSERT INTO servers (slug, url) VALUES ('woo', 'http://x/mcp')`)
	mustReject(t, db, "credential without auth", `INSERT INTO servers (slug, url, credential_enc) VALUES ('a', 'http://x/mcp', '\x01')`)
	mustReject(t, db, "bearer without credential", `INSERT INTO servers (slug, url, auth_type) VALUES ('b', 'http://x/mcp', 'bearer')`)
	mustReject(t, db, "header auth without header name", `INSERT INTO servers (slug, url, auth_type, credential_enc) VALUES ('c', 'http://x/mcp', 'header', '\x01')`)

	mustExec(t, db, `INSERT INTO audit_log (user_id, server_slug, tool, started_at, duration_ms, outcome)
		SELECT id, 'woo', 'list_orders', now(), 12, 'ok' FROM users`)
	mustExec(t, db, `DELETE FROM users`)
	if got := queryStrings(t, db, `SELECT coalesce(user_id::text, 'null') FROM audit_log`); !slices.Equal(got, []string{"null"}) {
		t.Errorf("audit rows after deleting their user = %v, want one row with a null user_id", got)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func mustReject(t *testing.T, db *sql.DB, what, query string) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), query); err == nil {
		t.Errorf("schema accepted %s", what)
	}
}

func queryStrings(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(fmt.Errorf("%s: %w", query, err))
	}
	return out
}
