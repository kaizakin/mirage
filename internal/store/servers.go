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
	// Internal marks a server the platform created, which may reach cluster-internal addresses.
	Internal bool
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

// SeedServer creates the server as internal unless one with its slug already exists, so an
// operator's later edits, such as disabling it, survive restarts. It reports whether it
// inserted.
//
// An existing row is marked internal again when it still has the seed's URL, which is how
// servers seeded before the flag existed get it. Matching the URL keeps trust with the
// address the operator configured: a dashboard-made server that took the slug, or a seeded
// one whose URL an admin changed, stays untrusted.
func (s *Store) SeedServer(ctx context.Context, box *secretbox.Box, seed ServerSeed) (bool, error) {
	credential, err := seal(box, seed.Slug, seed.Credential)
	if err != nil {
		return false, err
	}
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO servers (slug, url, auth_type, auth_header, credential_enc, internal)
		VALUES ($1, $2, $3, nullif($4, ''), $5, true)
		ON CONFLICT (slug) DO NOTHING`,
		seed.Slug, seed.URL, seed.AuthType, seed.AuthHeader, credential)
	if err != nil {
		return false, err
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return n == 1, err
	}
	_, err = s.db.ExecContext(ctx, `UPDATE servers SET internal = true WHERE slug = $1 AND url = $2 AND NOT internal`,
		seed.Slug, seed.URL)
	return false, err
}

// EnabledServers returns every enabled server, ordered by slug.
func (s *Store) EnabledServers(ctx context.Context) ([]Server, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT slug, url, auth_type, coalesce(auth_header, ''), credential_enc, internal
		FROM servers WHERE enabled ORDER BY slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var servers []Server
	for rows.Next() {
		var srv Server
		if err := rows.Scan(&srv.Slug, &srv.URL, &srv.AuthType, &srv.AuthHeader, &srv.CredentialEnc, &srv.Internal); err != nil {
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
	Internal      bool
	// Shop is the slug of the provisioned store that owns this server, or "". The store's
	// lifecycle manages such a server, not the dashboard's server controls.
	Shop string
}

// Servers lists every server, enabled or not, ordered by slug.
func (s *Store) Servers(ctx context.Context) ([]ServerRow, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.slug, s.url, s.enabled, s.auth_type, coalesce(s.auth_header, ''), s.credential_enc IS NOT NULL, s.created_at,
			s.internal, coalesce(sh.slug, '')
		FROM servers s LEFT JOIN shops sh ON sh.id = s.shop_id ORDER BY s.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerRow
	for rows.Next() {
		var r ServerRow
		if err := rows.Scan(&r.Slug, &r.URL, &r.Enabled, &r.AuthType, &r.AuthHeader, &r.HasCredential, &r.CreatedAt, &r.Internal, &r.Shop); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CreateServer adds an enabled server that is not internal. It returns ErrConflict for a taken slug and
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

// UpdateServer applies p to the server with slug. Changing an internal server's URL makes it
// untrusted: the platform vouched for the old address, not the new one.
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
			internal       = internal AND url = coalesce($2, url),
			enabled        = coalesce($3, enabled),
			auth_type      = coalesce($4, auth_type),
			auth_header    = CASE WHEN coalesce($4, auth_type) = 'header' THEN coalesce(nullif($5, ''), auth_header) END,
			credential_enc = CASE WHEN coalesce($4, auth_type) = 'none' THEN NULL ELSE coalesce($6, credential_enc) END
		WHERE slug = $1`,
		slug, p.URL, p.Enabled, p.AuthType, p.AuthHeader, credential)
	return affected(res, classify(err))
}

// ServerShop returns the slug of the store that owns the server, "" for an ordinary
// server, or ErrNotFound.
func (s *Store) ServerShop(ctx context.Context, slug string) (string, error) {
	var shop string
	err := s.db.QueryRowContext(ctx, `
		SELECT coalesce(sh.slug, '') FROM servers s LEFT JOIN shops sh ON sh.id = s.shop_id WHERE s.slug = $1`, slug).Scan(&shop)
	return shop, notFound(err)
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
