package store

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kaizakin/mirage/internal/gateway/policy"
)

func TestLiveToolRulesApplyToTheirSubjectAndFollowTheirServer(t *testing.T) {
	s := migrated(t)
	ctx := t.Context()
	ada, _ := s.CreateUser(ctx, "ada@example.com", "h", "member")
	bob, _ := s.CreateUser(ctx, "bob@example.com", "h", "member")
	if err := s.CreateServer(ctx, nil, ServerSeed{Slug: "store-tea", URL: "http://x", AuthType: "none"}); err != nil {
		t.Fatal(err)
	}

	seeded := []policy.Rule{{Tool: "create_product"}, {Tool: "update_order_status"}}
	got, err := s.ToolRulesFor(ctx, ada.ID)
	if err != nil || !sameRules(got, seeded) {
		t.Fatalf("seeded rules = %+v, %v; want members denied %v", got, err, seeded)
	}

	grant := policy.Rule{UserID: ada.ID, Server: "store-tea", Tool: "create_product", Allow: true}
	id, err := s.CreateToolRule(ctx, grant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateToolRule(ctx, grant); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate rule: err = %v, want ErrConflict", err)
	}
	if _, err := s.CreateToolRule(ctx, policy.Rule{Tool: "create_product"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate every-server rule: err = %v, want ErrConflict", err)
	}
	if _, err := s.CreateToolRule(ctx, policy.Rule{Server: "nope", Allow: false}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown server: err = %v, want ErrNotFound rather than a rule for every server", err)
	}

	if got, _ := s.ToolRulesFor(ctx, ada.ID); !sameRules(got, append(slices.Clone(seeded), grant)) {
		t.Fatalf("ada's rules = %+v; want the member rules and her grant", got)
	}
	if got, _ := s.ToolRulesFor(ctx, bob.ID); !sameRules(got, seeded) {
		t.Fatalf("bob's rules = %+v; ada's grant must not reach him", got)
	}
	all, err := s.ToolRules(ctx)
	if err != nil || len(all) != 3 || all[2].ID != id || all[2].UserEmail != "ada@example.com" {
		t.Fatalf("listing = %+v, %v", all, err)
	}

	if err := s.DeleteServer(ctx, "store-tea"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ToolRulesFor(ctx, ada.ID); !sameRules(got, seeded) {
		t.Fatalf("after its server was deleted, ada's rules = %+v; the grant must go with it", got)
	}
	if err := s.DeleteToolRule(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting a rule already gone: err = %v, want ErrNotFound", err)
	}
}

func sameRules(a, b []policy.Rule) bool {
	key := func(r policy.Rule) string { return r.Server + "/" + r.Tool }
	sort := func(rs []policy.Rule) []policy.Rule {
		rs = slices.Clone(rs)
		slices.SortFunc(rs, func(x, y policy.Rule) int {
			if x.UserID != y.UserID {
				return int(x.UserID - y.UserID)
			}
			return strings.Compare(key(x), key(y))
		})
		return rs
	}
	return slices.Equal(sort(a), sort(b))
}
