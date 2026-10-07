-- Manual-control desk: confirmed plan versions and the LP result behind
-- every published manifest («план / факт»). Mirror of
-- storage.InitEdgeSchema, which creates these tables idempotently at API
-- startup — apply manually only when running migrations by hand.

CREATE TABLE IF NOT EXISTS dispatch_versions (
    site_id text NOT NULL,
    version integer NOT NULL,
    confirmed_at timestamptz NOT NULL DEFAULT now(),
    confirmed_by text NOT NULL DEFAULT '',
    model jsonb NOT NULL,
    PRIMARY KEY (site_id, version)
);

CREATE TABLE IF NOT EXISTS dispatch_runs (
    site_id text NOT NULL,
    start_hour timestamptz NOT NULL,
    manifest_id text NOT NULL,
    run_at timestamptz NOT NULL DEFAULT now(),
    version integer NOT NULL,
    hours jsonb NOT NULL,
    PRIMARY KEY (site_id, start_hour, manifest_id)
);

CREATE INDEX IF NOT EXISTS dispatch_runs_site_run
    ON dispatch_runs (site_id, run_at DESC);
