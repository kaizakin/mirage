-- +goose Up
-- Dashboard login sessions. Only a hash of the cookie's token is stored, so a leaked table
-- cannot be replayed as cookies.
CREATE TABLE sessions (
    token_hash   bytea       PRIMARY KEY,
    user_id      bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX sessions_user_id_idx ON sessions (user_id);

-- +goose Down
DROP TABLE sessions;
