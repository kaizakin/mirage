package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/kaizakin/mirage/internal/auth"
)

// OAuthClient is a client that registered itself through dynamic client registration.
type OAuthClient struct {
	ID           string
	Name         string
	RedirectURIs []string
}

// CreateOAuthClient stores a newly registered client.
func (s *Store) CreateOAuthClient(ctx context.Context, c OAuthClient) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO oauth_clients (id, name, redirect_uris) VALUES ($1, $2, $3)`,
		c.ID, c.Name, c.RedirectURIs)
	return classify(err)
}

// OAuthClientByID returns a registered client, or ErrNotFound.
func (s *Store) OAuthClientByID(ctx context.Context, id string) (OAuthClient, error) {
	c := OAuthClient{ID: id}
	err := s.db.QueryRowContext(ctx, `SELECT name, redirect_uris FROM oauth_clients WHERE id = $1`, id).
		Scan(&c.Name, &c.RedirectURIs)
	return c, notFound(err)
}

// OAuthCode is an authorization code's request, kept until the client redeems it.
type OAuthCode struct {
	Hash          []byte
	UserID        int64
	ClientID      string
	Resource      string
	RedirectURI   string
	CodeChallenge string
	ExpiresAt     time.Time
}

// CreateOAuthCode records the user's consent as a grant and stores the code for it.
func (s *Store) CreateOAuthCode(ctx context.Context, c OAuthCode) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var grant int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO oauth_grants (user_id, client_id, resource) VALUES ($1, $2, $3) RETURNING id`,
		c.UserID, c.ClientID, c.Resource).Scan(&grant); err != nil {
		return classify(err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO oauth_codes (code_hash, grant_id, redirect_uri, code_challenge, expires_at) VALUES ($1, $2, $3, $4, $5)`,
		c.Hash, grant, c.RedirectURI, c.CodeChallenge, c.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}

// OAuthGrant is what a code or refresh token stands for.
type OAuthGrant struct {
	ID       int64
	UserID   int64
	ClientID string
	Resource string
}

// RedeemOAuthCode removes an unexpired code and returns its grant with the details the token
// request must match. A code can be redeemed once; a second attempt, or an expired or unknown
// code, is ErrNotFound.
func (s *Store) RedeemOAuthCode(ctx context.Context, hash []byte) (g OAuthGrant, redirectURI, challenge string, err error) {
	err = s.db.QueryRowContext(ctx, `
		WITH used AS (DELETE FROM oauth_codes WHERE code_hash = $1 AND expires_at > now()
		              RETURNING grant_id, redirect_uri, code_challenge)
		SELECT g.id, g.user_id, g.client_id, g.resource, used.redirect_uri, used.code_challenge
		FROM used JOIN oauth_grants g ON g.id = used.grant_id`, hash).
		Scan(&g.ID, &g.UserID, &g.ClientID, &g.Resource, &redirectURI, &challenge)
	return g, redirectURI, challenge, notFound(err)
}

// RedeemOAuthRefresh removes an unexpired refresh token and returns its grant, so each one
// works once: refreshing rotates it.
func (s *Store) RedeemOAuthRefresh(ctx context.Context, hash []byte) (OAuthGrant, error) {
	var g OAuthGrant
	err := s.db.QueryRowContext(ctx, `
		WITH used AS (DELETE FROM oauth_tokens WHERE token_hash = $1 AND kind = 'refresh' AND expires_at > now()
		              RETURNING grant_id)
		SELECT g.id, g.user_id, g.client_id, g.resource FROM used JOIN oauth_grants g ON g.id = used.grant_id`, hash).
		Scan(&g.ID, &g.UserID, &g.ClientID, &g.Resource)
	return g, notFound(err)
}

// IssuedToken is a token to store for a grant.
type IssuedToken struct {
	Hash      []byte
	Kind      string // "access" or "refresh"
	ExpiresAt time.Time
}

// StoreOAuthTokens stores tokens issued for a grant and marks the grant used.
func (s *Store) StoreOAuthTokens(ctx context.Context, grantID int64, tokens ...IssuedToken) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, t := range tokens {
		if _, err := tx.ExecContext(ctx, `INSERT INTO oauth_tokens (token_hash, grant_id, kind, expires_at) VALUES ($1, $2, $3, $4)`,
			t.Hash, grantID, t.Kind, t.ExpiresAt); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE oauth_grants SET last_used_at = now() WHERE id = $1`, grantID); err != nil {
		return err
	}
	return tx.Commit()
}

// OAuthAccess looks up an unexpired access token, implementing auth.OAuthTokens. An unknown
// or expired token is auth.ErrInvalidOAuthToken.
func (s *Store) OAuthAccess(ctx context.Context, hash []byte) (auth.OAuthAccess, error) {
	var a auth.OAuthAccess
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.email, u.role, g.resource, t.expires_at
		FROM oauth_tokens t JOIN oauth_grants g ON g.id = t.grant_id JOIN users u ON u.id = g.user_id
		WHERE t.token_hash = $1 AND t.kind = 'access' AND t.expires_at > now()`, hash).
		Scan(&a.User.ID, &a.User.Email, &a.User.Role, &a.Resource, &a.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return a, auth.ErrInvalidOAuthToken
	}
	return a, err
}

// ConnectedApp is a client a user has authorized.
type ConnectedApp struct {
	GrantID    int64
	ClientName string
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

// ConnectedApps lists a user's live grants, newest first. A grant whose tokens have all
// expired is no longer live.
func (s *Store) ConnectedApps(ctx context.Context, userID int64) ([]ConnectedApp, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, c.name, g.created_at, g.last_used_at
		FROM oauth_grants g JOIN oauth_clients c ON c.id = g.client_id
		WHERE g.user_id = $1 AND EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id AND t.expires_at > now())
		ORDER BY g.id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnectedApp
	for rows.Next() {
		var a ConnectedApp
		if err := rows.Scan(&a.GrantID, &a.ClientName, &a.CreatedAt, &a.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RevokeGrant ends one of a user's grants and every token issued under it.
func (s *Store) RevokeGrant(ctx context.Context, userID, grantID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM oauth_grants WHERE id = $1 AND user_id = $2`, grantID, userID)
	return affected(res, err)
}

// PruneOAuth deletes what can no longer be used: expired codes and tokens, grants left with
// neither for a day, and registered clients with no grant for a week. Registration needs no
// sign-in, so without this the clients table would only grow.
func (s *Store) PruneOAuth(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		DELETE FROM oauth_codes WHERE expires_at < now();
		DELETE FROM oauth_tokens WHERE expires_at < now();
		DELETE FROM oauth_grants g WHERE g.created_at < now() - interval '1 day'
			AND NOT EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.grant_id = g.id)
			AND NOT EXISTS (SELECT 1 FROM oauth_codes c WHERE c.grant_id = g.id);
		DELETE FROM oauth_clients c WHERE c.created_at < now() - interval '7 days'
			AND NOT EXISTS (SELECT 1 FROM oauth_grants g WHERE g.client_id = c.id)`)
	return err
}
