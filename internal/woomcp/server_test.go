package woomcp

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/woo"
)

// fakeStore is a WooCommerce stand-in that records every request it receives.
type fakeStore struct {
	mu       sync.Mutex
	requests []*http.Request
	bodies   []string
}

func (f *fakeStore) methods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.requests {
		out = append(out, r.Method)
	}
	return out
}

// connect serves handle as the store and returns a client session to a woo-mcp server backed by it.
func connect(t *testing.T, handle http.HandlerFunc) (*mcp.ClientSession, *fakeStore) {
	t.Helper()
	store := &fakeStore{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		store.mu.Lock()
		store.requests = append(store.requests, r)
		store.bodies = append(store.bodies, string(body))
		store.mu.Unlock()
		handle(w, r)
	}))
	t.Cleanup(srv.Close)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := NewServer(woo.New(srv.URL, "u", "p"), "test").Connect(t.Context(), serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(t.Context(), clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs, store
}

func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	return res
}

func text(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestListToolsAdvertisesStatusEnum(t *testing.T) {
	cs, _ := connect(t, http.NotFound)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	var listOrders *mcp.Tool
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Name == "list_orders" {
			listOrders = tool
		}
	}
	slices.Sort(names)
	want := []string{"create_product", "get_order", "list_orders", "list_products", "update_order_status"}
	if !slices.Equal(names, want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}

	raw, err := json.Marshal(listOrders.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	wantEnum := []string{"pending", "processing", "on-hold", "completed", "cancelled", "refunded", "failed"}
	if got := schema.Properties["status"].Enum; !slices.Equal(got, wantEnum) {
		t.Fatalf("status enum = %v, want %v", got, wantEnum)
	}
}

func TestCreateProductReturnsStructuredProduct(t *testing.T) {
	cs, store := connect(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/wp-json/wc/v3/products" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"id":42,"name":"Chai","regular_price":"299","status":"publish","date_created_gmt":"2026-10-20T04:00:00"}`)
	})

	res := call(t, cs, "create_product", map[string]any{"name": "Chai", "regular_price": "299"})
	if res.IsError {
		t.Fatalf("IsError: %s", text(res))
	}
	got, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("structuredContent = %#v", res.StructuredContent)
	}
	if got["id"] != 42.0 || got["name"] != "Chai" || got["date_created_gmt"] != "2026-10-20T04:00:00Z" {
		t.Fatalf("structuredContent = %v", got)
	}

	var sent woo.NewProduct
	if err := json.Unmarshal([]byte(store.bodies[0]), &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Name != "Chai" || sent.RegularPrice != "299" {
		t.Fatalf("POST body = %s", store.bodies[0])
	}
}

func TestCreateProductRejectsMalformedPrice(t *testing.T) {
	cs, store := connect(t, http.NotFound)

	res := call(t, cs, "create_product", map[string]any{"name": "Chai", "regular_price": "abc"})
	if !res.IsError || !strings.Contains(text(res), "regular_price") {
		t.Fatalf("IsError = %v, text = %q", res.IsError, text(res))
	}
	if slices.Contains(store.methods(), http.MethodPost) {
		t.Fatal("store received a POST for an invalid price")
	}
}

func TestGetOrderReportsStoreRejection(t *testing.T) {
	cs, _ := connect(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"code":"woocommerce_rest_shop_order_invalid_id","message":"Invalid ID.","data":{"status":404}}`)
	})

	res := call(t, cs, "get_order", map[string]any{"id": 999})
	if !res.IsError {
		t.Fatalf("IsError = false, text = %q", text(res))
	}
	want := "WooCommerce rejected the request (HTTP 404 woocommerce_rest_shop_order_invalid_id): Invalid ID."
	if !strings.Contains(text(res), want) {
		t.Fatalf("text = %q, want it to contain %q", text(res), want)
	}
}

func TestUpdateOrderStatusAcceptsOnlyKnownStatuses(t *testing.T) {
	cases := []struct {
		status  string
		wantPUT bool
	}{
		{"completed", true},
		{"bogus", false},
	}
	for _, tc := range cases {
		t.Run(tc.status, func(t *testing.T) {
			cs, store := connect(t, func(w http.ResponseWriter, _ *http.Request) {
				fmt.Fprintf(w, `{"id":7,"status":%q}`, tc.status)
			})

			res := call(t, cs, "update_order_status", map[string]any{"id": 7, "status": tc.status})
			if res.IsError == tc.wantPUT {
				t.Fatalf("IsError = %v, text = %q", res.IsError, text(res))
			}
			if got := slices.Contains(store.methods(), http.MethodPut); got != tc.wantPUT {
				t.Fatalf("store received PUT = %v, want %v", got, tc.wantPUT)
			}
		})
	}
}

func TestListOrdersTodayUsesStoreMidnight(t *testing.T) {
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	if err != nil {
		t.Fatal(err)
	}
	y, m, d := time.Now().In(kolkata).Date()
	wantAfter := time.Date(y, m, d, 0, 0, 0, 0, kolkata).UTC().Add(-time.Second).Format("2006-01-02T15:04:05")

	var gotAfter string
	cs, _ := connect(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wp-json/":
			fmt.Fprint(w, `{"timezone_string":"Asia/Kolkata","gmt_offset":5.5}`)
		case "/wp-json/wc/v3/orders":
			gotAfter = r.URL.Query().Get("after")
			w.Header().Set("X-WP-TotalPages", "1")
			fmt.Fprint(w, `[{"id":1,"status":"processing","total":"299.00"}]`)
		default:
			http.NotFound(w, r)
		}
	})

	res := call(t, cs, "list_orders", map[string]any{"after": "today"})
	if res.IsError {
		t.Fatalf("IsError: %s", text(res))
	}
	if gotAfter != wantAfter {
		t.Fatalf("after = %q, want %q", gotAfter, wantAfter)
	}
}

// The MCP spec requires every outputSchema to be an object; clients such as MCP Inspector drop
// tools that advertise anything else, and single-type dialects reject ["null", ...] unions.
func TestOutputSchemasAreSpecObjectsWithoutNullUnions(t *testing.T) {
	cs, _ := connect(t, http.NotFound)
	res, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		schema, ok := tool.OutputSchema.(map[string]any)
		if !ok || schema["type"] != "object" {
			t.Errorf("%s: outputSchema type = %v, want \"object\"", tool.Name, schema["type"])
		}
		for path, typ := range typeUnions("outputSchema", schema) {
			t.Errorf("%s: %s has type %v", tool.Name, path, typ)
		}
	}
}

// typeUnions returns every subschema path whose "type" is an array.
func typeUnions(path string, node any) map[string]any {
	found := map[string]any{}
	switch n := node.(type) {
	case map[string]any:
		if typ, ok := n["type"].([]any); ok {
			found[path] = typ
		}
		for k, v := range n {
			maps.Copy(found, typeUnions(path+"."+k, v))
		}
	case []any:
		for i, v := range n {
			maps.Copy(found, typeUnions(fmt.Sprintf("%s[%d]", path, i), v))
		}
	}
	return found
}
