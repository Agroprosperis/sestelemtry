-- Operator-entered hourly load plan for the cloud planner UI.
-- Mirror of storage.InitEdgeSchema (which creates this table
-- idempotently at API startup) — apply manually only when running
-- migrations by hand.

CREATE TABLE IF NOT EXISTS edge_load_plans (
    site_id text NOT NULL,
    hour timestamptz NOT NULL,
    load_kw double precision NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (site_id, hour)
);
