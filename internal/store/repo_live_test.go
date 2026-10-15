package store

import (
	"errors"
	"testing"

	"github.com/kaizakin/mirage/internal/auth"
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
