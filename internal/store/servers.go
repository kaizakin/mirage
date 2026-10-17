package store

import (
	"context"
	"time"

	"github.com/kaizakin/mirage/internal/secretbox"
)

// Server is one upstream MCP server row, with its credential still sealed.
type Server struct {
	Slug          string
	URL           string
	AuthType      string
	AuthHeader    string
	CredentialEnc []byte
}

// ServerSeed describes an upstream to create at startup. Credential is plaintext and is
// sealed before it reaches the database; it must be empty when AuthType is "none".
type ServerSeed struct {
	Slug       string `json:"slug"`
	URL        string `json:"url"`
	AuthType   string `json:"auth_type"`
	AuthHeader string `json:"auth_header,omitempty"`
	Credential string `json:"-"`
}

// SeedServer creates the server unless one with its slug already exists, so an operator's
// later edits, such as disabling it, survive restarts. It reports whether it inserted.
func (s *Store) SeedServer(ctx context.Context, box *secretbox.Box, seed ServerSeed) (bool, error) {
	credential, err := seal(box, seed.Slug, seed.Credential)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO servers (slug, url, auth_type, auth_header, credential_enc)
		VALUES ($1, $2, $3, nullif($4, ''), $5)
		ON CONFLICT (slug) DO NOTHING`,
		seed.Slug, seed.URL, seed.AuthType, seed.AuthHeader, credential)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// EnabledServers returns every enabled server, ordered by slug.
func (s *Store) EnabledServers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT slug, url, auth_type, coalesce(auth_header, ''), credential_enc
		FROM servers WHERE enabled ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []Server
	for rows.Next() {
		var srv Server
		if err := rows.Scan(&srv.Slug, &srv.URL, &srv.AuthType, &srv.AuthHeader, &srv.CredentialEnc); err != nil {
			return nil, err
		}
		servers = append(servers, srv)
	}
	return servers, rows.Err()
}

// ServerRow is a server as the dashboard lists it. It reports whether a credential is
// stored, never the credential itself.
type ServerRow struct {
	Slug          string
	URL           string
	Enabled       bool
	AuthType      string
	AuthHeader    string
	HasCredential bool
	CreatedAt     time.Time
}

// Servers lists every server, enabled or not, ordered by slug.
func (s *Store) Servers(ctx context.Context) ([]ServerRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT slug, url, enabled, auth_type, coalesce(auth_header, ''), credential_enc IS NOT NULL, created_at
		FROM servers ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerRow
	for rows.Next() {
		var r ServerRow
		if err := rows.Scan(&r.Slug, &r.URL, &r.Enabled, &r.AuthType, &r.AuthHeader, &r.HasCredential, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateServer adds an enabled server. It returns ErrConflict for a taken slug and
// ErrInvalid when the auth fields do not fit together.
func (s *Store) CreateServer(ctx context.Context, box *secretbox.Box, seed ServerSeed) error {
	credential, err := seal(box, seed.Slug, seed.Credential)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO servers (slug, url, auth_type, auth_header, credential_enc)
		VALUES ($1, $2, $3, nullif($4, ''), $5)`,
		seed.Slug, seed.URL, seed.AuthType, seed.AuthHeader, credential)
	return classify(err)
}

// ServerPatch changes some of a server's fields; nil fields stay as they are. A new
// AuthType of "none" clears the stored credential and header.
type ServerPatch struct {
	URL        *string
	Enabled    *bool
	AuthType   *string
	AuthHeader *string
	Credential *string
}

// UpdateServer applies p to the server with slug.
func (s *Store) UpdateServer(ctx context.Context, box *secretbox.Box, slug string, p ServerPatch) error {
	var credential []byte
	if p.Credential != nil {
		var err error
		if credential, err = seal(box, slug, *p.Credential); err != nil {
			return err
		}
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE servers SET
			url            = coalesce($2, url),
			enabled        = coalesce($3, enabled),
			auth_type      = coalesce($4, auth_type),
			auth_header    = CASE WHEN coalesce($4, auth_type) = 'header' THEN coalesce(nullif($5, ''), auth_header) END,
			credential_enc = CASE WHEN coalesce($4, auth_type) = 'none' THEN NULL ELSE coalesce($6, credential_enc) END
		WHERE slug = $1`,
		slug, p.URL, p.Enabled, p.AuthType, p.AuthHeader, credential)
	return affected(res, classify(err))
}

// DeleteServer removes a server. Its audit rows remain.
func (s *Store) DeleteServer(ctx context.Context, slug string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM servers WHERE slug = $1`, slug)
	return affected(res, err)
}

// seal encrypts a credential bound to its server's slug; an empty credential stays nil.
func seal(box *secretbox.Box, slug, credential string) ([]byte, error) {
	if credential == "" {
		return nil, nil
	}
	return box.Encrypt([]byte(credential), []byte(slug))
}
