-- +goose Up
-- An internal server was created by the platform itself (a provisioned store's, or one the
-- operator lists in SEED_SERVERS), so the gateway lets it reach cluster-internal addresses.
-- Servers added through the dashboard stay false and may only reach public addresses.
-- Seeded servers regain the flag when the gateway next seeds them on startup.
ALTER TABLE servers ADD COLUMN internal boolean NOT NULL DEFAULT false;
UPDATE servers SET internal = true WHERE shop_id IS NOT NULL;

-- +goose Down
ALTER TABLE servers DROP COLUMN internal;
