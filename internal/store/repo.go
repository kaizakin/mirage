package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kaizakin/mirage/internal/auth"
	"github.com/kaizakin/mirage/internal/gateway/audit"
)

// Store is the gateway's repository over its Postgres tables.
type Store struct {
	db *sql.DB
}

// New returns a Store over a migrated database.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// CreateUser inserts a user and returns it.
func (s *Store) CreateUser(ctx context.Context, email, passwordHash, role string) (auth.User, error) {
	u := auth.User{Email: email, Role: role}
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO users (email, password_hash, role) VALUES ($1, $2, $3) RETURNING id`,
		email, passwordHash, role).Scan(&u.ID)
	return u, err
}

// CreateAPIKey stores a key for userID. Only the id and the secret's hash are kept.
func (s *Store) CreateAPIKey(ctx context.Context, userID int64, id string, secretHash []byte) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_keys (user_id, key_prefix, secret_hash) VALUES ($1, $2, $3)`,
		userID, id, secretHash)
	return err
}

// RevokeAPIKey marks a key revoked. Revoking an already revoked key keeps its first revocation time.
func (s *Store) RevokeAPIKey(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE api_keys SET revoked_at = coalesce(revoked_at, now()) WHERE key_prefix = $1`, id)
	return err
}

// APIKeyByID implements auth.KeyStore.
func (s *Store) APIKeyByID(ctx context.Context, id string) (auth.KeyRecord, error) {
	var rec auth.KeyRecord
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.role, k.secret_hash, k.revoked_at IS NOT NULL
		FROM api_keys k JOIN users u ON u.id = k.user_id
		WHERE k.key_prefix = $1`, id).
		Scan(&rec.User.ID, &rec.User.Email, &rec.User.Role, &rec.SecretHash, &rec.Revoked)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.KeyRecord{}, auth.ErrKeyNotFound
	}
	return rec, err
}

// RecordCall implements audit.Recorder.
func (s *Store) RecordCall(ctx context.Context, e audit.Entry) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO audit_log (user_id, server_slug, tool, started_at, duration_ms, outcome, error)
		VALUES (nullif($1, 0), $2, $3, $4, $5, $6, nullif($7, ''))`,
		e.UserID, e.ServerSlug, e.Tool, e.StartedAt, e.Duration.Milliseconds(), string(e.Outcome), e.Error)
	return err
}

// SeedAdmin creates the first admin and, if keyID is set, their API key, skipping whichever
// already exists, so it is safe on every startup. It reports what it created.
func (s *Store) SeedAdmin(ctx context.Context, email, passwordHash, keyID string, keySecretHash []byte) (userCreated, keyCreated bool, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, role) VALUES ($1, $2, 'admin')
		ON CONFLICT ((lower(email))) DO NOTHING`, email, passwordHash)
	if err != nil {
		return false, false, err
	}
	n, _ := res.RowsAffected()
	userCreated = n == 1

	if keyID != "" {
		res, err = tx.ExecContext(ctx, `
			INSERT INTO api_keys (user_id, key_prefix, secret_hash)
			SELECT id, $2, $3 FROM users WHERE lower(email) = lower($1)
			ON CONFLICT (key_prefix) DO NOTHING`, email, keyID, keySecretHash)
		if err != nil {
			return false, false, err
		}
		n, _ = res.RowsAffected()
		keyCreated = n == 1
	}
	return userCreated, keyCreated, tx.Commit()
}
