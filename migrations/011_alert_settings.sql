-- Alert delivery settings edited on the dashboard's notifications page.
-- The alert-watchdog reads these every check, so a change in the UI takes
-- effect without restarting the container; the `alerts:` block in
-- config.yaml is only the fallback for a deployment that has never saved
-- the form. Mirrored by storage.InitAlertSettingsSchema.

-- Site-wide settings: a single row (the CHECK pins it) holding the SMTP
-- server, thresholds and the default recipient list.
CREATE TABLE IF NOT EXISTS alert_settings (
    id            smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    -- Everything except the password, as one JSONB blob so new knobs
    -- don't need an ALTER TABLE (same trade-off as organization_tariffs).
    settings      jsonb NOT NULL,
    -- The password lives in its own column precisely so it is NOT part of
    -- that blob: the API's read path never selects this column, which
    -- makes leaking the secret through a settings response structurally
    -- impossible rather than a matter of remembering to strip a field.
    smtp_password text NOT NULL DEFAULT '',
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- Per-organization overrides. A row exists only for organizations the
-- operator has actually touched; everyone else inherits the global list
-- and stays enabled, so adding a site to config.yaml never leaves it
-- silently unmonitored.
CREATE TABLE IF NOT EXISTS organization_alert_settings (
    organization_id text PRIMARY KEY,
    settings        jsonb NOT NULL,
    updated_at      timestamptz NOT NULL DEFAULT now()
);
