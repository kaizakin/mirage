package store

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"
)

// A rolled-back gateway starts against a schema migrated by a newer one: helm rollback never
// runs a down migration. Its embedded migrations are then a prefix of the database's, and
// starting must still succeed (docs/upgrade-rollback.md).
func TestOlderGatewayStartsAgainstNewerSchema(t *testing.T) {
	db := freshDB(t)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}

	all, err := fs.Glob(embedded, "migrations/*.sql")
	if err != nil || len(all) < 2 {
		t.Fatalf("migrations: %v, %v", all, err)
	}
	older := fstest.MapFS{}
	for _, name := range all[:len(all)-1] {
		data, err := fs.ReadFile(embedded, name)
		if err != nil {
			t.Fatal(err)
		}
		older[strings.TrimPrefix(name, "migrations/")] = &fstest.MapFile{Data: data}
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, older)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(t.Context()); err != nil {
		t.Fatalf("an older gateway's migrations against the newer schema: %v", err)
	}
}
