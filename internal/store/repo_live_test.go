package store

import (
	"errors"
	"testing"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/secretbox"
)

func TestLiveAPIKeysVerifyAgainstPostgres(t *testing.T) {
	db := freshDB(t)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	s := New(db)

	user, err := s.CreateUser(t.Context(), "ada@example.com", "hash", "admin")
	if err != nil {
		t.Fatal(err)
	}
	key, err := auth.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateAPIKey(t.Context(), user.ID, key.ID, key.SecretHash); err != nil {
		t.Fatal(err)
	}

	got, err := auth.VerifyKey(t.Context(), s, key.Plaintext)
	if err != nil || got != user {
		t.Fatalf("VerifyKey = %+v, %v; want %+v", got, err, user)
	}

	if err := s.RevokeAPIKey(t.Context(), key.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.VerifyKey(t.Context(), s, key.Plaintext); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("after revoke: err = %v, want ErrInvalidKey", err)
	}

	other, _ := auth.GenerateKey()
	if _, err := auth.VerifyKey(t.Context(), s, other.Plaintext); !errors.Is(err, auth.ErrInvalidKey) {
		t.Fatalf("unknown key: err = %v, want ErrInvalidKey", err)
	}
}

func TestLiveSeedServersIsIdempotentAndListsEnabled(t *testing.T) {
	db := freshDB(t)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	box, err := secretbox.New(make([]byte, secretbox.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	seeds := []ServerSeed{
		{Slug: "deepwiki", URL: "https://mcp.deepwiki.com/mcp", AuthType: "none"},
		{Slug: "currency", URL: "http://cur:8080/mcp", AuthType: "header", AuthHeader: "X-Api-Key", Credential: "k"},
	}
	for _, seed := range seeds {
		if inserted, err := s.SeedServer(t.Context(), box, seed); err != nil || !inserted {
			t.Fatalf("first seed of %s: inserted=%v err=%v", seed.Slug, inserted, err)
		}
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE servers SET enabled = false WHERE slug = 'deepwiki'`); err != nil {
		t.Fatal(err)
	}
	// A restart re-seeds: existing rows, including the operator's disable, must survive.
	for _, seed := range seeds {
		if inserted, err := s.SeedServer(t.Context(), box, seed); err != nil || inserted {
			t.Fatalf("re-seed of %s: inserted=%v err=%v", seed.Slug, inserted, err)
		}
	}

	servers, err := s.EnabledServers(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Slug != "currency" || servers[0].AuthHeader != "X-Api-Key" {
		t.Fatalf("enabled servers = %+v, want only currency", servers)
	}
	credential, err := box.Decrypt(servers[0].CredentialEnc, []byte("currency"))
	if err != nil || string(credential) != "k" {
		t.Fatalf("stored credential opens to %q, %v; want \"k\" bound to its slug", credential, err)
	}
}

func TestLiveSeedAdminIsIdempotentAndItsKeyVerifies(t *testing.T) {
	db := freshDB(t)
	if err := Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	s := New(db)
	key, _ := auth.GenerateKey()

	userCreated, keyCreated, err := s.SeedAdmin(t.Context(), "Admin@Example.com", "hash", key.ID, key.SecretHash)
	if err != nil || !userCreated || !keyCreated {
		t.Fatalf("first seed: user=%v key=%v err=%v", userCreated, keyCreated, err)
	}
	userCreated, keyCreated, err = s.SeedAdmin(t.Context(), "admin@example.com", "other", key.ID, key.SecretHash)
	if err != nil || userCreated || keyCreated {
		t.Fatalf("re-seed: user=%v key=%v err=%v; want nothing created", userCreated, keyCreated, err)
	}
	u, err := auth.VerifyKey(t.Context(), s, key.Plaintext)
	if err != nil || u.Role != "admin" || u.Email != "Admin@Example.com" {
		t.Fatalf("seeded key verifies as %+v, %v", u, err)
	}
}
