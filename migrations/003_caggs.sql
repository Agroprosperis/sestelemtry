-- Continuous aggregates for telemetry samples.
--
-- Rationale: month/year dashboard queries scan ~720 / ~8760 hours of raw
-- 5-second samples per metric for every page load (≈3M rows for one
-- month × 6 metrics). A TimescaleDB continuous aggregate at the hourly
-- granularity reduces the same query to ≤720 rows per metric and lets the
-- API re-bucket cheaply on read.
--
-- The hourly CAGG keeps `last(value, time)` for monotonic counters
-- (energy_*_kwh) and `avg(value)` for instantaneous metrics (SOC, power).
-- Day-preset queries (5-minute buckets) still hit the raw hypertable.
--
-- Real-time aggregation is left enabled (default) so queries above the
-- watermark transparently UNION materialized rows with on-the-fly
-- aggregation of raw data. start_offset = NULL backfills everything below
-- the watermark on first refresh.

CREATE MATERIALIZED VIEW IF NOT EXISTS telemetry_samples_hourly
WITH (timescaledb.continuous) AS
SELECT
    time_bucket(INTERVAL '1 hour', time) AS hour,
    organization_id,
    metric_key,
    last(value, time) AS last_value,
    avg(value)        AS avg_value,
    count(*)          AS sample_count
FROM telemetry_samples
GROUP BY hour, organization_id, metric_key
WITH NO DATA;

SELECT add_continuous_aggregate_policy(
    'telemetry_samples_hourly',
    start_offset => NULL,
    end_offset   => INTERVAL '15 minutes',
    schedule_interval => INTERVAL '5 minutes',
    if_not_exists => TRUE
);

CREATE INDEX IF NOT EXISTS telemetry_samples_hourly_org_metric_hour
    ON telemetry_samples_hourly (organization_id, metric_key, hour DESC);

-- Backfill the existing range once. Subsequent refreshes are incremental.
-- Safe to re-run: refresh_continuous_aggregate is idempotent for the
-- region it covers.
CALL refresh_continuous_aggregate('telemetry_samples_hourly', NULL, NULL);
