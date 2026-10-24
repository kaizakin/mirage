package store

import (
	"context"
	"time"

	"github.com/kaizakin/mirage/internal/gateway/policy"
)

// ToolRule is a rule as the dashboard lists it: UserEmail is "" for a rule covering every
// member, and Server or Tool is "" for every server or tool.
type ToolRule struct {
	ID        int64
	UserID    int64
	UserEmail string
	Server    string
	Tool      string
	Allow     bool
	CreatedAt time.Time
}

// ToolRules lists every rule, broadest first.
func (s *Store) ToolRules(ctx context.Context) ([]ToolRule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT r.id, coalesce(r.user_id, 0), coalesce(u.email, ''), coalesce(s.slug, ''), coalesce(r.tool, ''), r.allow, r.created_at
		FROM tool_rules r LEFT JOIN users u ON u.id = r.user_id LEFT JOIN servers s ON s.id = r.server_id
		ORDER BY r.user_id NULLS FIRST, u.email, s.slug NULLS FIRST, r.tool NULLS FIRST`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ToolRule
	for rows.Next() {
		var r ToolRule
		if err := rows.Scan(&r.ID, &r.UserID, &r.UserEmail, &r.Server, &r.Tool, &r.Allow, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ToolRulesFor returns the rules that may apply to a member: those for every member and the
// member's own.
func (s *Store) ToolRulesFor(ctx context.Context, userID int64) ([]policy.Rule, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT coalesce(r.user_id, 0), coalesce(s.slug, ''), coalesce(r.tool, ''), r.allow
		FROM tool_rules r LEFT JOIN servers s ON s.id = r.server_id
		WHERE r.user_id IS NULL OR r.user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []policy.Rule
	for rows.Next() {
		var r policy.Rule
		if err := rows.Scan(&r.UserID, &r.Server, &r.Tool, &r.Allow); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateToolRule adds a rule and returns its ID. It returns ErrNotFound for an unknown
// server, ErrInvalid for an unknown user, and ErrConflict when a rule with the same subject,
// server, and tool exists.
func (s *Store) CreateToolRule(ctx context.Context, r policy.Rule) (int64, error) {
	var id int64
	// An unknown slug must not fall through to a NULL server_id, which means every server.
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO tool_rules (user_id, server_id, tool, allow)
		SELECT nullif($1::bigint, 0), s.id, nullif($3, ''), $4
		FROM (VALUES (1)) v LEFT JOIN servers s ON s.slug = $2
		WHERE $2 = '' OR s.id IS NOT NULL
		RETURNING id`, r.UserID, r.Server, r.Tool, r.Allow).Scan(&id)
	return id, notFound(classify(err))
}

// DeleteToolRule removes a rule, or returns ErrNotFound.
func (s *Store) DeleteToolRule(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM tool_rules WHERE id = $1`, id)
	return affected(res, err)
}
