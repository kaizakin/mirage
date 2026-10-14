-- +goose Up
CREATE TABLE users (
    id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email         text        NOT NULL,
    password_hash text        NOT NULL,
    role          text        NOT NULL CHECK (role IN ('admin', 'member')),
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX users_email_key ON users (lower(email));

CREATE TABLE invites (
    token_hash bytea       PRIMARY KEY,
    email      text        NOT NULL,
    role       text        NOT NULL CHECK (role IN ('admin', 'member')),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);

CREATE TABLE api_keys (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The non-secret leading part of the key, looked up on every request.
    key_prefix   text        NOT NULL UNIQUE,
    secret_hash  bytea       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    revoked_at   timestamptz,
    last_used_at timestamptz
);
CREATE INDEX api_keys_user_id_idx ON api_keys (user_id);

CREATE TABLE servers (
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug           text        NOT NULL UNIQUE,
    url            text        NOT NULL,
    enabled        boolean     NOT NULL DEFAULT true,
    auth_type      text        NOT NULL DEFAULT 'none' CHECK (auth_type IN ('none', 'bearer', 'header')),
    -- Header name for auth_type 'header', such as X-Api-Key.
    auth_header    text,
    -- secretbox-sealed credential, with the slug as associated data.
    credential_enc bytea,
    created_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT servers_credential_matches_auth CHECK ((auth_type = 'none') = (credential_enc IS NULL)),
    CONSTRAINT servers_header_matches_auth CHECK ((auth_type = 'header') = (auth_header IS NOT NULL))
);

CREATE TABLE audit_log (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- Kept when the user is deleted, so the trail survives.
    user_id     bigint      REFERENCES users (id) ON DELETE SET NULL,
    -- A slug, not a foreign key: rows outlive renamed or deleted servers.
    server_slug text        NOT NULL,
    tool        text        NOT NULL,
    started_at  timestamptz NOT NULL,
    duration_ms integer     NOT NULL CHECK (duration_ms >= 0),
    outcome     text        NOT NULL,
    error       text
);
CREATE INDEX audit_log_started_at_idx ON audit_log (started_at DESC);

-- +goose Down
DROP TABLE audit_log;
DROP TABLE servers;
DROP TABLE api_keys;
DROP TABLE invites;
DROP TABLE users;
