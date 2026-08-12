-- 012_edge.sql — EMS edge ingest (shadow MVP).
-- Mirror of storage.InitEdgeSchema (internal/storage/edge.go); the
-- schema is normally created idempotently at API startup, this file
-- exists for manual/reviewable deployments per the repo convention.

-- Idempotency ledger: one row per accepted uplink batch.
CREATE TABLE IF NOT EXISTS edge_batches (
    batch_id text PRIMARY KEY,
    site_id text NOT NULL,
    edge_id text,
    sent_at timestamptz,
    received_at timestamptz NOT NULL DEFAULT now(),
    records int NOT NULL DEFAULT 0,
    control_records int NOT NULL DEFAULT 0,
    events int NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS edge_batches_site_received
    ON edge_batches (site_id, received_at DESC);

-- Shadow-engine decisions (ems-spec MVP §9.3): typed columns for SQL
-- analytics + the full canonical record for audit/replay.
CREATE TABLE IF NOT EXISTS control_decisions (
    time timestamptz NOT NULL,
    site_id text NOT NULL,
    mode text,
    preset text,
    state_machine text,
    plan_source text,
    reason_code text,
    rationale text,
    p_bess_virtual_kw double precision,
    p_pv_limit_virtual_kw double precision,
    record jsonb NOT NULL,
    batch_id text
);
CREATE INDEX IF NOT EXISTS control_decisions_site_time
    ON control_decisions (site_id, time DESC);
SELECT create_hypertable('control_decisions', 'time', if_not_exists => TRUE, migrate_data => TRUE);

-- Edge events (SL_POLL_FAIL, SHADOW_ANOMALY, DISPATCH_DEGRADED, ...).
CREATE TABLE IF NOT EXISTS edge_events (
    time timestamptz NOT NULL,
    site_id text NOT NULL,
    severity text,
    code text,
    message text,
    context jsonb,
    batch_id text
);
CREATE INDEX IF NOT EXISTS edge_events_site_time
    ON edge_events (site_id, time DESC);

-- Liveness: one row per site, last writer wins.
CREATE TABLE IF NOT EXISTS edge_heartbeats (
    site_id text PRIMARY KEY,
    edge_id text,
    status text,
    buffer_pending bigint,
    last_sl_poll_ok timestamptz,
    firmware_version text,
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Published manifests (manifest-lite). The newest issued_at row per
-- site is what GET /api/v1/edge/manifest serves.
CREATE TABLE IF NOT EXISTS edge_manifests (
    site_id text NOT NULL,
    manifest_id text NOT NULL,
    payload jsonb NOT NULL,
    issued_at timestamptz NOT NULL DEFAULT now(),
    valid_from timestamptz,
    valid_until timestamptz,
    published boolean NOT NULL DEFAULT true,
    PRIMARY KEY (site_id, manifest_id)
);
CREATE INDEX IF NOT EXISTS edge_manifests_site_issued
    ON edge_manifests (site_id, issued_at DESC);
