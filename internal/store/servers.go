package store

import (
	"context"

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
	var credential []byte
	if seed.Credential != "" {
		var err error
		if credential, err = box.Encrypt([]byte(seed.Credential), []byte(seed.Slug)); err != nil {
			return false, err
		}
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
