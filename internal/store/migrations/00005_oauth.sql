-- +goose Up
-- OAuth 2.1 for MCP clients that log in through a browser (the MCP authorization flow),
-- alongside API keys. Codes and tokens are stored only as SHA-256 hashes.

-- Clients register themselves (RFC 7591). All are public clients using PKCE.
CREATE TABLE oauth_clients (
    id            text        PRIMARY KEY,
    name          text        NOT NULL,
    redirect_uris text[]      NOT NULL CHECK (cardinality(redirect_uris) BETWEEN 1 AND 5),
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- A grant is one user's consent for one client. Revoking it ends all of its tokens.
CREATE TABLE oauth_grants (
    id           bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    client_id    text        NOT NULL REFERENCES oauth_clients (id) ON DELETE CASCADE,
    resource     text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);
CREATE INDEX oauth_grants_user_id_idx ON oauth_grants (user_id);

-- Authorization codes live a minute and are used once.
CREATE TABLE oauth_codes (
    code_hash      bytea       PRIMARY KEY,
    grant_id       bigint      NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    redirect_uri   text        NOT NULL,
    code_challenge text        NOT NULL,
    expires_at     timestamptz NOT NULL
);

CREATE TABLE oauth_tokens (
    token_hash bytea       PRIMARY KEY,
    grant_id   bigint      NOT NULL REFERENCES oauth_grants (id) ON DELETE CASCADE,
    kind       text        NOT NULL CHECK (kind IN ('access', 'refresh')),
    expires_at timestamptz NOT NULL
);
CREATE INDEX oauth_tokens_grant_id_idx ON oauth_tokens (grant_id);

-- +goose Down
DROP TABLE oauth_tokens;
DROP TABLE oauth_codes;
DROP TABLE oauth_grants;
DROP TABLE oauth_clients;
