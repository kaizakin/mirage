-- +goose Up
-- Provisioned WooCommerce stores (Shop in Go, since package store is this database layer).
-- A row is the desired state; the provisioner reconciles the cluster toward it. Each store
-- lives in namespace store-<slug>, so the slug is short and DNS-safe.
CREATE TABLE shops (
    id                 bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug               text        NOT NULL UNIQUE
                                   CONSTRAINT shops_slug_format CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 20),
    name               text        NOT NULL CONSTRAINT shops_name_length CHECK (length(name) BETWEEN 1 AND 60),
    owner_id           bigint      REFERENCES users (id) ON DELETE SET NULL,
    host               text        NOT NULL,
    status             text        NOT NULL DEFAULT 'provisioning'
                                   CHECK (status IN ('provisioning', 'ready', 'failed', 'deleting')),
    reason             text,
    -- The store's generated passwords and tokens, sealed with secretbox. They are made once,
    -- so a reinstall after a crash matches the data already on the store's volumes.
    secrets_enc        bytea,
    -- When the current provisioning attempt began; the provisioner's timeout runs from here.
    provisioning_since timestamptz NOT NULL DEFAULT now(),
    created_at         timestamptz NOT NULL DEFAULT now(),
    ready_at           timestamptz
);
CREATE INDEX shops_owner_id_idx ON shops (owner_id);

-- A store's MCP server is registered as an ordinary upstream that belongs to the store, and
-- goes when the store's row does.
ALTER TABLE servers ADD COLUMN shop_id bigint REFERENCES shops (id) ON DELETE CASCADE;

-- +goose Down
ALTER TABLE servers DROP COLUMN shop_id;
DROP TABLE shops;
