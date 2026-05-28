-- Replace the hourly continuous aggregate with a daily one. The dashboard
-- only ever reads month/year presets at >= 1 day granularity, so the
-- hourly resolution introduced by migration 003 is 24× larger than what
-- the API actually needs. Daily buckets aligned to Europe/Kyiv local
-- midnight match the dashboard's TZ exactly: month preset reads them
-- as-is, year preset re-buckets daily into monthly with another
-- time_bucket() on read.
--
-- The view stores `first(value, time)`, `last(value, time)`, `avg(value)`
-- and `count(*)` so that:
--   * delta queries can fall back to in-bucket `last - first` when the
--     pre-period seed sample is missing (fresh-deploy first day).
--   * /energy-summary can read three indexed values per metric (end,
--     before-seed, in-period-first) without touching the raw hypertable.

DROP MATERIALIZED VIEW IF EXISTS telemetry_samples_hourly CASCADE;

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_samples_daily
WITH (timescaledb.continuous) AS
SELECT
    time_bucket(INTERVAL '1 day', time, 'Europe/Kyiv') AS day,
    organization_id,
    metric_key,
    first(value, time) AS first_value,
    last(value, time)  AS last_value,
    avg(value)         AS avg_value,
    count(*)           AS sample_count
FROM telemetry_samples
GROUP BY day, organization_id, metric_key
WITH NO DATA;

-- Enable real-time aggregation so queries that fall in the
-- 15-minute window between refresh runs (or today's bucket, which
-- the policy intentionally never refreshes until tomorrow because
-- of `end_offset => 15 minutes`) still see fresh raw rows merged in
-- on read. Without this the /energy-summary endpoint reads today's
-- accumulated counters as the previous day's values and the day
-- preset's "СЕС згенерувала" / "Заряд УЗЕ" cards render 0 until
-- midnight rolls over. TimescaleDB 2.13+ defaults this to TRUE
-- (materialized-only); we explicitly flip it back so the dashboard
-- always reads up-to-the-second data regardless of refresh cadence.
ALTER MATERIALIZED VIEW telemetry_samples_daily
    SET (timescaledb.materialized_only = false);

SELECT add_continuous_aggregate_policy(
    'telemetry_samples_daily',
    start_offset => NULL,
    end_offset   => INTERVAL '15 minutes',
    schedule_interval => INTERVAL '15 minutes',
    if_not_exists => TRUE
);

CREATE INDEX IF NOT EXISTS telemetry_samples_daily_org_metric_day
    ON telemetry_samples_daily (organization_id, metric_key, day DESC);

CALL refresh_continuous_aggregate('telemetry_samples_daily', NULL, NULL);
