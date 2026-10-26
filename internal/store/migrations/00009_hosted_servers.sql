-- +goose Up
-- A hosted server is a server the platform runs from a container image, in namespace
-- mcp-<slug>. Its servers row routes calls as usual; this row is the provisioner's desired
-- state, and deleting the server deletes it, after which the provisioner removes the namespace.
CREATE TABLE hosted_servers (
    server_id           bigint      PRIMARY KEY REFERENCES servers (id) ON DELETE CASCADE,
    image               text        NOT NULL CHECK (length(image) BETWEEN 1 AND 255),
    port                integer     NOT NULL CHECK (port BETWEEN 1 AND 65535),
    path                text        NOT NULL CHECK (path ~ '^/'),
    -- The container's environment as sealed JSON, with "hosted-env:<slug>" as associated data.
    env_enc             bytea,
    status              text        NOT NULL DEFAULT 'deploying' CHECK (status IN ('deploying', 'running', 'failed')),
    reason              text,
    -- Bumped by every change to the image or environment; the provisioner upgrades the release
    -- until deployed_generation catches up.
    generation          integer     NOT NULL DEFAULT 1,
    deployed_generation integer     NOT NULL DEFAULT 0,
    -- When the current deployment began; the provisioner's timeout runs from here.
    deploying_since     timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE hosted_servers;
