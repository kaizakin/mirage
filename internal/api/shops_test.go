package api

import (
	"testing"

	"github.com/kaizakin/mirage/internal/store"
)

func TestStoreURLUsesTheConfiguredScheme(t *testing.T) {
	a := &API{Options: Options{Shops: ShopOptions{Scheme: "https"}}}
	if got := a.toShopJSON(store.Shop{Slug: "tea", Host: "tea.shop.example"}).URL; got != "https://tea.shop.example" {
		t.Fatalf("url = %q", got)
	}
}
