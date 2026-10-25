package naming

import (
	"strings"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct{ slug, tool string }{
		{"woo", "list_orders"},
		{"currency", "slow_echo"},
		{"deep-wiki", "ask_wiki_question"},
		// An upstream tool name containing the separator still decodes to the right server.
		{"woo", "export__csv"},
	}
	for _, c := range cases {
		name, err := Encode(c.slug, c.tool)
		if err != nil {
			t.Fatalf("Encode(%q, %q): %v", c.slug, c.tool, err)
		}
		slug, tool, ok := Decode(name)
		if !ok || slug != c.slug || tool != c.tool {
			t.Errorf("Decode(%q) = %q, %q, %v; want %q, %q", name, slug, tool, ok, c.slug, c.tool)
		}
	}
}

func TestEncodeRejectsNamesClientsWouldReject(t *testing.T) {
	cases := map[string][2]string{
		"dot in tool":     {"woo", "orders.list"},
		"space in tool":   {"woo", "list orders"},
		"empty tool":      {"woo", ""},
		"too long":        {"woo", strings.Repeat("x", 62)},
		"underscore slug": {"my_server", "tool"},
		"uppercase slug":  {"Woo", "tool"},
	}
	for name, c := range cases {
		if got, err := Encode(c[0], c[1]); err == nil {
			t.Errorf("%s: Encode(%q, %q) = %q, want an error", name, c[0], c[1], got)
		}
	}
}

func TestDecodeRejectsNamesNotFromEncode(t *testing.T) {
	for _, name := range []string{"list_orders", "woo__", "__tool", "Woo__tool", "my_srv__tool"} {
		if slug, tool, ok := Decode(name); ok {
			t.Errorf("Decode(%q) = %q, %q, true; want not ok", name, slug, tool)
		}
	}
}

func TestValidateSlug(t *testing.T) {
	for _, ok := range []string{"woo", "currency", "deep-wiki", "a1"} {
		if err := ValidateSlug(ok); err != nil {
			t.Errorf("ValidateSlug(%q): %v", ok, err)
		}
	}
	for _, bad := range []string{"", "-woo", "woo-", "deep--wiki", "woo_mcp", "WOO", "mirage", strings.Repeat("a", 33)} {
		if ValidateSlug(bad) == nil {
			t.Errorf("ValidateSlug(%q) accepted it", bad)
		}
	}
}
