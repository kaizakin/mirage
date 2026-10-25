package downstream

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/kaizakin/mirage/internal/gateway/audit"
)

func TestSearchModeListsTwoToolsThatFindAndCallTheRest(t *testing.T) {
	h := setup(t, Options{Policy: fixedRules{{Tool: "get_order", Allow: false}}})
	cs := connectAs(t, h, "member", toolModeHeader, searchMode)

	if got, want := listedNames(t, cs), []string{"mirage__call_tool", "mirage__search_tools"}; !slices.Equal(got, want) {
		t.Fatalf("search mode lists %v, want %v", got, want)
	}
	if got := listedNames(t, connectAs(t, h, "member")); slices.Contains(got, "mirage__search_tools") {
		t.Fatalf("the full list %v carries the search tools", got)
	}

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "mirage__search_tools", Arguments: map[string]any{"query": "list the orders"}})
	if err != nil || res.IsError {
		t.Fatalf("search: %v %s", err, text(res))
	}
	var found searchOutput
	if err := json.Unmarshal([]byte(text(res)), &found); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range found.Tools {
		names = append(names, f.Name)
		if f.InputSchema == nil {
			t.Errorf("%s came back without its input schema", f.Name)
		}
	}
	// get_order would match "orders" too, but the member may not use it.
	if want := []string{"woo__list_orders"}; !slices.Equal(names, want) {
		t.Fatalf("search found %v, want %v", names, want)
	}

	call := func(name string) *mcp.CallToolResult {
		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "mirage__call_tool",
			Arguments: map[string]any{"name": name, "arguments": map[string]any{"text": "hi"}}})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if res := call("woo__list_orders"); res.IsError || text(res) != "woo/list_orders:hi" {
		t.Fatalf("call_tool woo__list_orders: isError %v, %q", res.IsError, text(res))
	}
	if res := call("woo__get_order"); !res.IsError {
		t.Fatalf("call_tool reached a denied tool: %q", text(res))
	}
	if res := call("mirage__call_tool"); !res.IsError || text(res) != "unknown tool mirage__call_tool; find tools with mirage__search_tools" {
		t.Fatalf("call_tool on itself: isError %v, %q", res.IsError, text(res))
	}

	var got []string
	for _, e := range h.audit.all() {
		got = append(got, e.ServerSlug+"__"+e.Tool+" "+string(e.Outcome))
	}
	if want := []string{"woo__list_orders " + string(audit.OK), "woo__get_order " + string(audit.Denied)}; !slices.Equal(got, want) {
		t.Fatalf("audit = %v, want the real tools' rows %v", got, want)
	}
}

func TestRankPrefersWholeNameWords(t *testing.T) {
	tools := []*mcp.Tool{
		{Name: "a__get_order", Description: "One order by ID."},
		{Name: "b__convert", Description: "Convert an amount between currencies, for an order total."},
		{Name: "c__list_orders", Description: "Recent orders."},
		{Name: "d__ping"},
	}
	var names []string
	for _, f := range rank(tools, "order", 10) {
		names = append(names, f.Name)
	}
	if want := []string{"a__get_order", "c__list_orders", "b__convert"}; !slices.Equal(names, want) {
		t.Fatalf("rank = %v, want %v", names, want)
	}
	if got := rank(tools, "order", 1); len(got) != 1 {
		t.Fatalf("limit 1 returned %d tools", len(got))
	}
}
