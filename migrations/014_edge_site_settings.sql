-- Per-site planner/control settings edited in the control console
-- (SOC policy, power limits, grid limits). Mirror of
-- storage.InitEdgeSchema, which creates this table idempotently at API
-- startup — apply manually only when running migrations by hand.

CREATE TABLE IF NOT EXISTS edge_site_settings (
    site_id text PRIMARY KEY,
    payload jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
