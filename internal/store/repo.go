package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kaizakin/mirage/internal/auth"
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
