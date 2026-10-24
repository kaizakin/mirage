// Package policy decides which tools a user may use. The gateway applies one decision in two
// places, hiding tools from tools/list and refusing tools/call, so the two always agree.
//
// Admins may use every tool. For a member, every rule that matches a call is a candidate and
// the most specific wins: a rule for the user beats one for all members, then a named server
// beats every server, then a named tool beats every tool. A tool no rule matches is allowed.
package policy

import (
	"context"

	"github.com/kaizakin/mirage/internal/auth"
)

// Rule allows or denies tools to every member (UserID 0) or one user. An empty Server or
// Tool matches every server or tool.
type Rule struct {
	UserID int64
	Server string
	Tool   string
	Allow  bool
}

// Source returns the rules that may apply to a member: those for every member and the
// member's own.
type Source interface {
	ToolRulesFor(ctx context.Context, userID int64) ([]Rule, error)
}

// Policy is one user's decision function.
type Policy struct {
	all   bool
	rules []Rule
}

// AllowAll is the policy of a caller every tool is open to.
var AllowAll = Policy{all: true}

// For returns u's policy.
func For(ctx context.Context, src Source, u auth.User) (Policy, error) {
	if u.Role == "admin" {
		return AllowAll, nil
	}
	rules, err := src.ToolRulesFor(ctx, u.ID)
	if err != nil {
		return Policy{}, err
	}
	return Policy{rules: rules}, nil
}

// Allows reports whether the policy lets its user call tool on server.
func (p Policy) Allows(server, tool string) bool {
	if p.all {
		return true
	}
	allow, best := true, -1
	for _, r := range p.rules {
		if (r.Server != "" && r.Server != server) || (r.Tool != "" && r.Tool != tool) {
			continue
		}
		if s := specificity(r); s > best {
			allow, best = r.Allow, s
		}
	}
	return allow
}

func specificity(r Rule) int {
	s := 0
	if r.UserID != 0 {
		s += 4
	}
	if r.Server != "" {
		s += 2
	}
	if r.Tool != "" {
		s++
	}
	return s
}
