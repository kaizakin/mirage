package store

import (
	"errors"
	"sync"
	"testing"

	"github.com/kaizakin/mirage/internal/secretbox"
)

func TestLiveCreateShopIsSafeToRepeat(t *testing.T) {
	s := migrated(t)
	ada, _ := s.CreateUser(t.Context(), "ada@example.com", "h", "member")
	bob, _ := s.CreateUser(t.Context(), "bob@example.com", "h", "member")
	req := NewShop{Slug: "tea", Name: "Tea House", Host: "tea.example", OwnerID: ada.ID, MaxPerOwner: 3}

	first, created, err := s.CreateShop(t.Context(), req)
	if err != nil || !created || first.Status != ShopProvisioning || first.OwnerEmail != "ada@example.com" {
		t.Fatalf("create = %+v, %v, %v", first, created, err)
	}
	again, created, err := s.CreateShop(t.Context(), req)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("repeat = %+v, created %v, %v; want the same store back", again, created, err)
	}
	if _, _, err := s.CreateShop(t.Context(), NewShop{Slug: "tea", Name: "Mine", Host: "tea.example", OwnerID: bob.ID, MaxPerOwner: 3}); !errors.Is(err, ErrConflict) {
		t.Fatalf("bob taking ada's slug: err = %v, want ErrConflict", err)
	}

	if err := s.MarkShopFailed(t.Context(), first.ID, "timed out"); err != nil {
		t.Fatal(err)
	}
	retried, created, err := s.CreateShop(t.Context(), req)
	if err != nil || created || retried.Status != ShopProvisioning || retried.Reason != "" {
		t.Fatalf("retry of a failed store = %+v, %v, %v; want it provisioning again", retried, created, err)
	}

	if err := s.MarkShopDeleting(t.Context(), first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateShop(t.Context(), req); !errors.Is(err, ErrConflict) {
		t.Fatalf("create while deleting: err = %v, want ErrConflict", err)
	}
}

func TestLiveShopLimitHoldsUnderConcurrentCreates(t *testing.T) {
	s := migrated(t)
	ada, _ := s.CreateUser(t.Context(), "ada@example.com", "h", "member")
	var wg sync.WaitGroup
	results := make(chan error, 5)
	for _, slug := range []string{"a", "b", "c", "d", "e"} {
		wg.Go(func() {
			_, _, err := s.CreateShop(t.Context(), NewShop{Slug: slug, Name: slug, Host: slug, OwnerID: ada.ID, MaxPerOwner: 2})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	var ok, limited int
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrShopLimit):
			limited++
		default:
			t.Fatal(err)
		}
	}
	if ok != 2 || limited != 3 {
		t.Fatalf("%d created and %d refused; want 2 and 3", ok, limited)
	}
}

func TestLiveShopReadinessAndTeardown(t *testing.T) {
	s := migrated(t)
	box, _ := secretbox.New(make([]byte, secretbox.KeySize))
	ada, _ := s.CreateUser(t.Context(), "ada@example.com", "h", "member")
	shop, _, err := s.CreateShop(t.Context(), NewShop{Slug: "tea", Name: "Tea", Host: "tea.example", OwnerID: ada.ID, MaxPerOwner: 3})
	if err != nil {
		t.Fatal(err)
	}
	srv := ShopServer{Slug: "store-tea", URL: "http://woo/mcp", Credential: "token"}

	if stored, err := s.SetShopSecrets(t.Context(), shop.ID, []byte("first")); err != nil || string(stored) != "first" {
		t.Fatalf("set secrets = %q, %v", stored, err)
	}
	if stored, _ := s.SetShopSecrets(t.Context(), shop.ID, []byte("second")); string(stored) != "first" {
		t.Fatalf("a second writer got %q; secrets must be made once", stored)
	}

	if err := s.MarkShopReady(t.Context(), box, shop.ID, srv); err != nil {
		t.Fatal(err)
	}
	enabled, _ := s.EnabledServers(t.Context())
	if len(enabled) != 1 || enabled[0].Slug != "store-tea" {
		t.Fatalf("enabled servers = %+v, want the store's", enabled)
	}
	if err := s.CreateServer(t.Context(), box, ServerSeed{Slug: "store-other", URL: "http://x", AuthType: "none"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RegisterShopServer(t.Context(), box, shop.ID, ShopServer{Slug: "store-other", URL: "http://x", Credential: "token"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("taking over a server the store does not own: err = %v, want ErrConflict", err)
	}

	if err := s.MarkShopDeleting(t.Context(), shop.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkShopReady(t.Context(), box, shop.ID, srv); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ready after delete was asked: err = %v, want ErrNotFound", err)
	}
	if err := s.DeleteShop(t.Context(), shop.ID); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := s.EnabledServers(t.Context()); len(enabled) != 1 || enabled[0].Slug != "store-other" {
		t.Fatalf("after deleting the store, servers = %+v; its registration must go with it", enabled)
	}
}
