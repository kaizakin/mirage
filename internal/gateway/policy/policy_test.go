package policy

import (
	"context"
	"errors"
	"testing"

	"github.com/kaizakin/mirage/internal/auth"
)

type rules []Rule

func (r rules) ToolRulesFor(context.Context, int64) ([]Rule, error) { return r, nil }

func TestMostSpecificRuleDecides(t *testing.T) {
	const ada = 7
	src := rules{
		{Tool: "create_product", Allow: false},
		{Tool: "update_order_status", Allow: false},
		{Server: "deepwiki", Allow: false},
		{Server: "deepwiki", Tool: "read_wiki_structure", Allow: true},
		{UserID: ada, Server: "store-tea", Allow: true},
		{UserID: ada, Server: "currency", Allow: false},
	}
	cases := []struct {
		server, tool string
		want         bool
	}{
		{"store-tea", "list_orders", true},         // no rule matches
		{"store-coffee", "create_product", false},  // a members rule on every server
		{"store-tea", "create_product", true},      // the user's rule beats the members rule
		{"store-tea", "update_order_status", true}, // a user rule for every tool still beats a members rule
		{"deepwiki", "ask_wiki_question", false},   // a server denied
		{"deepwiki", "read_wiki_structure", true},  // one tool opened on a denied server
		{"currency", "convert", false},             // a server denied to one user
	}
	p, err := For(t.Context(), src, auth.User{ID: ada, Role: "member"})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		if got := p.Allows(c.server, c.tool); got != c.want {
			t.Errorf("Allows(%s, %s) = %v, want %v", c.server, c.tool, got, c.want)
		}
	}
}

type failing struct{}

func (failing) ToolRulesFor(context.Context, int64) ([]Rule, error) { return nil, errors.New("down") }

func TestAdminsMayUseEveryToolWithoutReadingRules(t *testing.T) {
	p, err := For(t.Context(), failing{}, auth.User{ID: 1, Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Allows("store-tea", "create_product") {
		t.Fatal("an admin was refused a tool")
	}
	if _, err := For(t.Context(), failing{}, auth.User{ID: 2, Role: "member"}); err == nil {
		t.Fatal("a member's policy was built without their rules")
	}
}
