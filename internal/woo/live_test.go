package woo

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestLiveStore runs against a real store, e.g. through
//
//	kubectl -n mirage port-forward svc/mirage-wordpress 8081:80
//
// with WOO_USERNAME and WOO_PASSWORD taken from the woo-api-credentials Secret.
// WOO_BASE_URL defaults to http://localhost:8081.
func TestLiveStore(t *testing.T) {
	username, password := os.Getenv("WOO_USERNAME"), os.Getenv("WOO_PASSWORD")
	if password == "" {
		t.Skip("WOO_PASSWORD not set; skipping live store test")
	}
	baseURL := os.Getenv("WOO_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8081"
	}
	c := New(baseURL, username, password)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	name := "Live Test Mug " + strconv.FormatInt(time.Now().UnixNano(), 36)
	product, err := c.CreateProduct(ctx, NewProduct{Name: name, RegularPrice: "149"})
	if err != nil {
		t.Fatalf("CreateProduct: %v", err)
	}
	t.Cleanup(func() { forceDelete(t, c, "/products/"+strconv.Itoa(product.ID)) })
	if product.ID == 0 || product.RegularPrice != "149" {
		t.Fatalf("created product = %+v", product)
	}

	products, err := c.ListProducts(ctx)
	if err != nil {
		t.Fatalf("ListProducts: %v", err)
	}
	if !containsID(products, product.ID, func(p Product) int { return p.ID }) {
		t.Fatalf("product %d missing from ListProducts (%d products)", product.ID, len(products))
	}

	// The client has no CreateOrder; place one through the raw API so "today" has something to find.
	var placed Order
	_, err = c.do(ctx, http.MethodPost, apiPrefix+"/orders", nil, map[string]any{
		"status":         StatusProcessing,
		"payment_method": "cod",
		"line_items":     []map[string]int{{"product_id": product.ID, "quantity": 2}},
	}, &placed)
	if err != nil {
		t.Fatalf("placing order: %v", err)
	}
	t.Cleanup(func() { forceDelete(t, c, "/orders/"+strconv.Itoa(placed.ID)) })

	today, err := c.StartOfToday(ctx)
	if err != nil {
		t.Fatalf("StartOfToday: %v", err)
	}
	t.Logf("store's today starts at %s (%s UTC)", today, today.UTC())

	orders, err := c.ListOrders(ctx, today, StatusProcessing)
	if err != nil {
		t.Fatalf("ListOrders: %v", err)
	}
	t.Logf("%d processing order(s) today", len(orders))
	if !containsID(orders, placed.ID, func(o Order) int { return o.ID }) {
		t.Fatalf("order %d missing from today's processing orders", placed.ID)
	}

	// The same order must not show up when "after" is tomorrow.
	later, err := c.ListOrders(ctx, today.AddDate(0, 0, 1), "")
	if err != nil {
		t.Fatalf("ListOrders(tomorrow): %v", err)
	}
	if containsID(later, placed.ID, func(o Order) int { return o.ID }) {
		t.Fatalf("order %d listed as created after tomorrow", placed.ID)
	}

	got, err := c.GetOrder(ctx, placed.ID)
	if err != nil {
		t.Fatalf("GetOrder: %v", err)
	}
	if got.Total != "298.00" || len(got.LineItems) != 1 || got.LineItems[0].Quantity != 2 {
		t.Fatalf("GetOrder = total %q, items %+v", got.Total, got.LineItems)
	}

	updated, err := c.UpdateOrderStatus(ctx, placed.ID, StatusCompleted)
	if err != nil {
		t.Fatalf("UpdateOrderStatus: %v", err)
	}
	if updated.Status != StatusCompleted {
		t.Fatalf("status after update = %q", updated.Status)
	}
}

func containsID[T any](items []T, id int, idOf func(T) int) bool {
	for _, it := range items {
		if idOf(it) == id {
			return true
		}
	}
	return false
}

func forceDelete(t *testing.T, c *Client, path string) {
	t.Helper()
	q := url.Values{"force": {"true"}}
	if _, err := c.do(context.Background(), http.MethodDelete, apiPrefix+path, q, nil, nil); err != nil {
		t.Errorf("cleanup %s: %v", path, err)
	}
}
