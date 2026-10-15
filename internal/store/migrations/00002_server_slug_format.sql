-- +goose Up
-- Mirrors naming.ValidateSlug: without '_', a slug can be split back out of <slug>__<tool>.
ALTER TABLE servers ADD CONSTRAINT servers_slug_format
    CHECK (slug ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(slug) <= 32);

-- +goose Down
ALTER TABLE servers DROP CONSTRAINT servers_slug_format;
