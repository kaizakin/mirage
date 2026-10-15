package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeKeys map[string]KeyRecord

func (f fakeKeys) APIKeyByID(_ context.Context, id string) (KeyRecord, error) {
	rec, ok := f[id]
	if !ok {
		return KeyRecord{}, ErrKeyNotFound
	}
	return rec, nil
}

var ada = User{ID: 1, Email: "ada@example.com", Role: "admin"}

func issue(t *testing.T, store fakeKeys, revoked bool) string {
	t.Helper()
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store[k.ID] = KeyRecord{User: ada, SecretHash: k.SecretHash, Revoked: revoked}
	return k.Plaintext
}

func TestVerifyKeyAcceptsValidKey(t *testing.T) {
	store := fakeKeys{}
	key := issue(t, store, false)
	got, err := VerifyKey(t.Context(), store, key)
	if err != nil || got != ada {
		t.Fatalf("VerifyKey = %+v, %v; want %+v", got, err, ada)
	}
}

func TestVerifyKeyRejectsWrongSecret(t *testing.T) {
	store := fakeKeys{}
	key := issue(t, store, false)
	other, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	// This key's id with another key's (well-formed) secret.
	forged := key[:len("gw_")+9] + other.Plaintext[len("gw_")+9:]
	if _, err := VerifyKey(t.Context(), store, forged); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("wrong secret: err = %v, want ErrInvalidKey", err)
	}
}

func TestVerifyKeyRejectsRevokedKey(t *testing.T) {
	store := fakeKeys{}
	key := issue(t, store, true)
	if _, err := VerifyKey(t.Context(), store, key); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("revoked key: err = %v, want ErrInvalidKey", err)
	}
}

func TestVerifyKeyRejectsUnknownID(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyKey(t.Context(), fakeKeys{}, k.Plaintext); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("unknown id: err = %v, want ErrInvalidKey", err)
	}
}

func TestVerifyKeyRejectsMalformedKeys(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	store := fakeKeys{k.ID: {User: ada, SecretHash: k.SecretHash}}
	secret := k.Plaintext[len("gw_")+9:]
	cases := map[string]string{
		"empty":                "",
		"bare secret":          secret,
		"wrong scheme":         "sk_" + k.Plaintext[3:],
		"truncated":            k.Plaintext[:len(k.Plaintext)-1],
		"extra character":      k.Plaintext + "A",
		"missing separator":    "gw_" + k.ID + "x" + secret,
		"uppercase id":         "gw_" + strings.ToUpper(k.ID) + "_" + secret,
		"id outside alphabet":  "gw_" + k.ID[:7] + "1_" + secret,
		"secret not base64":    "gw_" + k.ID + "_" + secret[:42] + "!",
		"non-canonical secret": "gw_" + k.ID + "_" + secret[:42] + noncanonical(secret[42]),
		"bearer prefix kept":   "Bearer " + k.Plaintext,
	}
	for name, key := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyKey(t.Context(), store, key); !errors.Is(err, ErrInvalidKey) {
				t.Fatalf("err = %v, want ErrInvalidKey", err)
			}
		})
	}
}

type brokenStore struct{}

func (brokenStore) APIKeyByID(context.Context, string) (KeyRecord, error) {
	return KeyRecord{}, errors.New("connection refused")
}

func TestVerifyKeyReportsStoreOutageAsOutage(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	_, err = VerifyKey(t.Context(), brokenStore{}, k.Plaintext)
	if err == nil || errors.Is(err, ErrInvalidKey) {
		t.Fatalf("store outage: err = %v, want a non-ErrInvalidKey error", err)
	}
}

func TestGeneratedKeysAreDistinctAndHashKeyAgrees(t *testing.T) {
	a, _ := GenerateKey()
	b, _ := GenerateKey()
	if a.Plaintext == b.Plaintext || a.ID == b.ID {
		t.Fatalf("two generated keys collide: %q %q", a.Plaintext, b.Plaintext)
	}
	if len(a.Plaintext) != keyLen || !strings.HasPrefix(a.Plaintext, "gw_") {
		t.Fatalf("key %q is not gw_<8>_<43>", a.Plaintext)
	}
	id, hash, err := HashKey(a.Plaintext)
	if err != nil || id != a.ID || string(hash) != string(a.SecretHash) {
		t.Fatalf("HashKey = %q, %x, %v; want %q, %x", id, hash, err, a.ID, a.SecretHash)
	}
}

// noncanonical returns c with one of the two unused low bits of the final base64url
// character set: lenient decoders read the same bytes, strict ones reject it.
func noncanonical(c byte) string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	return string(alphabet[strings.IndexByte(alphabet, c)|1])
}
