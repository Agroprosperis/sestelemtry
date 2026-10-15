-- 020: counter stitching. SmartLogger's lifetime energy registers fall
-- or freeze when it loses a device and jump back when the link
-- returns; economics-recompute writes the corrections that keep them
-- continuous, and every counter delta query adds counter_offset() to
-- the raw reading.
-- Mirror of storage.InitCounterSchema, which the API and
-- economics-recompute run at startup — apply manually only when running
-- migrations by hand.
-- Apply locally with: supabase migration up

CREATE TABLE IF NOT EXISTS counter_corrections (
    organization_id text NOT NULL,
    metric_key      text NOT NULL,
    starts_at       timestamptz NOT NULL,
    ends_at         timestamptz,
    offset_start    double precision NOT NULL,
    offset_end      double precision NOT NULL,
    PRIMARY KEY (organization_id, metric_key, starts_at)
);

CREATE TABLE IF NOT EXISTS counter_episodes (
    organization_id   text NOT NULL,
    metric_key        text NOT NULL,
    started_at        timestamptz NOT NULL,
    kind              text NOT NULL,
    ended_at          timestamptz,
    value_before      double precision NOT NULL DEFAULT 0,
    value_after       double precision NOT NULL DEFAULT 0,
    value_returned    double precision,
    gap_kwh           double precision,
    detected_at       timestamptz NOT NULL DEFAULT now(),
    notified_open_at  timestamptz,
    notified_close_at timestamptz,
    PRIMARY KEY (organization_id, metric_key, started_at)
);

CREATE INDEX IF NOT EXISTS counter_episodes_org_started
    ON counter_episodes (organization_id, started_at);

CREATE TABLE IF NOT EXISTS counter_stitch_state (
    organization_id text NOT NULL,
    metric_key      text NOT NULL,
    processed_until timestamptz NOT NULL,
    state           jsonb NOT NULL DEFAULT '{}'::jsonb,
    updated_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, metric_key)
);

CREATE TABLE IF NOT EXISTS counter_stitch_rebuilds (
    organization_id text PRIMARY KEY,
    changed_from    timestamptz NOT NULL,
    requested_at    timestamptz NOT NULL DEFAULT now()
);

CREATE OR REPLACE FUNCTION counter_offset(org text, metric text, ts timestamptz)
RETURNS double precision
LANGUAGE sql STABLE PARALLEL SAFE AS $fn$
    SELECT COALESCE((
        SELECT CASE
            WHEN c.ends_at IS NULL OR c.ends_at <= c.starts_at OR ts <= c.starts_at THEN c.offset_start
            WHEN ts >= c.ends_at THEN c.offset_end
            ELSE c.offset_start + (c.offset_end - c.offset_start)
                * (EXTRACT(epoch FROM ts - c.starts_at) / EXTRACT(epoch FROM c.ends_at - c.starts_at))
        END
        FROM counter_corrections c
        WHERE c.organization_id = org
            AND c.metric_key = metric
            AND c.starts_at <= ts
        ORDER BY c.starts_at DESC
        LIMIT 1
    ), 0)
$fn$;
