-- Per-day planned PV generation, cached from the n8n forecast flow so
-- month/year plan-vs-actual costs one indexed read instead of one
-- upstream call per day. Mirror of storage.InitPvPlanSchema, which
-- creates this table idempotently at API startup — apply manually only
-- when running migrations by hand.
--
-- planned_kwh = 0 records that the flow was asked and had no forecast
-- for that day; fetched_at is what lets those misses be retried on a
-- slow cadence instead of on every dashboard poll.

CREATE TABLE IF NOT EXISTS pv_plan_daily (
    organization_id text NOT NULL,
    day             date NOT NULL,
    planned_kwh     double precision NOT NULL,
    fetched_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, day)
);
