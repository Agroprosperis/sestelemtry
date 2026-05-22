-- Per-organization economics-page tariff settings.
-- Populated by the API itself (PUT /api/v1/organization-tariffs) — this
-- is the first user-writable table exposed over HTTP, replacing the
-- previous URL-only persistence on the daily economics dashboard.
--
-- Schema is intentionally minimal: a single JSONB blob per org keeps
-- the contract on the API side (validate fields in Go) and lets us add
-- new tariff knobs without an ALTER TABLE round-trip. PK on
-- organization_id ensures last-writer-wins via ON CONFLICT upsert.
--
-- Mirrored programmatically by storage.InitTariffsSchema, so a fresh
-- environment without the migration file (e.g. local dev) still works.
CREATE TABLE IF NOT EXISTS organization_tariffs (
    organization_id text NOT NULL,
    tariffs         jsonb NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id)
);
