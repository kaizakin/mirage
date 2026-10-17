package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kaizakin/mirage/internal/auth"
)

// Errors callers map to HTTP statuses.
var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("already exists")
	// ErrInvalid wraps a database constraint the request broke; its message names the rule.
	ErrInvalid = errors.New("invalid")
)

// classify turns Postgres constraint errors into ErrConflict and ErrInvalid.
func classify(err error) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch pg.Code {
	case "23505": // unique_violation
		return fmt.Errorf("%w: %s", ErrConflict, pg.ConstraintName)
	case "23514", "23502", "23503": // check, not-null, foreign key
		return fmt.Errorf("%w: %s", ErrInvalid, pg.ConstraintName)
	}
	return err
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

// Member is a user as the dashboard lists them.
type Member struct {
	auth.User
	CreatedAt time.Time
}

// UserByEmail returns a user and their bcrypt password hash, matching email case-insensitively.
func (s *Store) UserByEmail(ctx context.Context, email string) (auth.User, string, error) {
	var u auth.User
	var hash string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, email, role, password_hash FROM users WHERE lower(email) = lower($1)`, email).
		Scan(&u.ID, &u.Email, &u.Role, &hash)
	return u, hash, notFound(err)
}

// Members lists every user, oldest first.
func (s *Store) Members(ctx context.Context) ([]Member, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, email, role, created_at FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Member
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.ID, &m.Email, &m.Role, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetRole changes a user's role.
func (s *Store) SetRole(ctx context.Context, userID int64, role string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE users SET role = $2 WHERE id = $1`, userID, role)
	return affected(res, classify(err))
}

// DeleteUser removes a user with their sessions and API keys; their audit rows remain.
func (s *Store) DeleteUser(ctx context.Context, userID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = $1`, userID)
	return affected(res, err)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CreateSession stores a login session that expires after ttl.
func (s *Store) CreateSession(ctx context.Context, tokenHash []byte, userID int64, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, now() + $3 * interval '1 second')`,
		tokenHash, userID, ttl.Seconds())
	return err
}

// SessionUser returns the user of a live session and marks it used. A session is live until
// its absolute expiry and while it has been used within idle.
func (s *Store) SessionUser(ctx context.Context, tokenHash []byte, idle time.Duration) (auth.User, error) {
	var u auth.User
	err := s.db.QueryRowContext(ctx, `
		UPDATE sessions s SET last_seen_at = now()
		FROM users u
		WHERE s.token_hash = $1 AND u.id = s.user_id
		  AND s.expires_at > now() AND s.last_seen_at > now() - $2 * interval '1 second'
		RETURNING u.id, u.email, u.role`, tokenHash, idle.Seconds()).
		Scan(&u.ID, &u.Email, &u.Role)
	return u, notFound(err)
}

// DeleteSession ends a session. Ending one that does not exist is not an error.
func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// Invite is a pending invitation. TokenHash identifies it safely: it cannot be used to accept.
type Invite struct {
	TokenHash []byte
	Email     string
	Role      string
	ExpiresAt time.Time
}

// CreateInvite stores an invitation, keeping only its token's hash.
func (s *Store) CreateInvite(ctx context.Context, tokenHash []byte, email, role string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO invites (token_hash, email, role, expires_at) VALUES ($1, $2, $3, $4)`,
		tokenHash, email, role, expiresAt)
	return classify(err)
}

// PendingInvites lists unused, unexpired invitations, newest first.
func (s *Store) PendingInvites(ctx context.Context) ([]Invite, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT token_hash, email, role, expires_at FROM invites
		WHERE used_at IS NULL AND expires_at > now() ORDER BY expires_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Invite
	for rows.Next() {
		var inv Invite
		if err := rows.Scan(&inv.TokenHash, &inv.Email, &inv.Role, &inv.ExpiresAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// DeleteInvite withdraws an invitation.
func (s *Store) DeleteInvite(ctx context.Context, tokenHash []byte) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM invites WHERE token_hash = $1`, tokenHash)
	return affected(res, err)
}

// AcceptInvite creates the invited user and marks the invite used, atomically: of two
// concurrent accepts of one invite, only one creates an account. It returns ErrNotFound for
// an unknown, used, or expired invite and ErrConflict if the email already has an account.
func (s *Store) AcceptInvite(ctx context.Context, tokenHash []byte, passwordHash string) (auth.User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return auth.User{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var u auth.User
	err = tx.QueryRowContext(ctx, `
		SELECT email, role FROM invites
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		FOR UPDATE`, tokenHash).Scan(&u.Email, &u.Role)
	if err != nil {
		return auth.User{}, notFound(err)
	}
	if err := tx.QueryRowContext(ctx,
		`INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		u.Email, passwordHash, u.Role).Scan(&u.ID); err != nil {
		return auth.User{}, classify(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE invites SET used_at = now() WHERE token_hash = $1`, tokenHash); err != nil {
		return auth.User{}, err
	}
	return u, tx.Commit()
}

// APIKey describes a stored key; the secret is never stored, so it is never here.
type APIKey struct {
	ID         string
	UserID     int64
	UserEmail  string
	CreatedAt  time.Time
	RevokedAt  *time.Time
	LastUsedAt *time.Time
}

// APIKeys lists keys, newest first: one user's, or everyone's when userID is 0.
func (s *Store) APIKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT k.key_prefix, u.id, u.email, k.created_at, k.revoked_at, k.last_used_at
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE $1 = 0 OR k.user_id = $1
		ORDER BY k.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.UserEmail, &k.CreatedAt, &k.RevokedAt, &k.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeAPIKeyOf revokes key id if it belongs to userID, or any user's when userID is 0.
func (s *Store) RevokeAPIKeyOf(ctx context.Context, id string, userID int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE api_keys SET revoked_at = coalesce(revoked_at, now())
		WHERE key_prefix = $1 AND ($2 = 0 OR user_id = $2)`, id, userID)
	return affected(res, err)
}

// TouchAPIKey records that a key was just used, at most once a minute per key.
func (s *Store) TouchAPIKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE api_keys SET last_used_at = now()
		WHERE key_prefix = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, id)
	return err
}

// AuditFilter selects audit rows. Zero fields do not filter. Rows come newest first, with
// IDs below Before when it is set.
type AuditFilter struct {
	UserID  int64
	Server  string
	Tool    string
	Outcome string
	Before  int64
	Limit   int
}

// AuditRow is one recorded tool call.
type AuditRow struct {
	ID         int64
	UserEmail  string
	ServerSlug string
	Tool       string
	StartedAt  time.Time
	DurationMS int
	Outcome    string
	Error      string
}

// Audit returns the audit rows matching f, newest first.
func (s *Store) Audit(ctx context.Context, f AuditFilter) ([]AuditRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.id, coalesce(u.email, ''), a.server_slug, a.tool, a.started_at, a.duration_ms, a.outcome, coalesce(a.error, '')
		FROM audit_log a LEFT JOIN users u ON u.id = a.user_id
		WHERE ($1 = 0 OR a.user_id = $1)
		  AND ($2 = '' OR a.server_slug = $2)
		  AND ($3 = '' OR a.tool = $3)
		  AND ($4 = '' OR a.outcome = $4)
		  AND ($5 = 0 OR a.id < $5)
		ORDER BY a.id DESC
		LIMIT $6`, f.UserID, f.Server, f.Tool, f.Outcome, f.Before, f.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditRow
	for rows.Next() {
		var r AuditRow
		if err := rows.Scan(&r.ID, &r.UserEmail, &r.ServerSlug, &r.Tool, &r.StartedAt, &r.DurationMS, &r.Outcome, &r.Error); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
