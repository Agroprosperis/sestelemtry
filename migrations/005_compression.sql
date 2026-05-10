-- Enable TimescaleDB native compression on telemetry_samples and schedule
-- a background policy that compresses chunks older than 7 days. Also
-- applied programmatically by the collector via storage.InitCompression
-- on every startup, so this file is for documentation and manual replay.
--
-- Rationale: production cadence is 1Hz × ~7 (organization_id, metric_key)
-- pairs ≈ 5M rows/day ≈ 1.15 GB/day on the raw hypertable, with the
-- (organization_id, metric_key, time DESC) index doubling that footprint.
-- Columnar compression on these slowly-changing doubles, segmented by
-- (organization_id, metric_key), reproducibly hits ~18-25× total size
-- reduction (heap collapses under Gorilla + delta-of-delta, the row-level
-- index is replaced by per-segment min/max metadata so its space is
-- recovered too).
--
-- The 7-day threshold keeps the day-preset chart (5-minute buckets over
-- the last ~24h) reading uncompressed data while month/year presets
-- transparently read compressed history. New inserts always land in the
-- active (uncompressed) chunk; the policy worker compresses older chunks
-- in the background without blocking writes.

ALTER TABLE telemetry_samples SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'organization_id, metric_key',
    timescaledb.compress_orderby   = 'time DESC'
);

SELECT add_compression_policy(
    'telemetry_samples',
    INTERVAL '7 days',
    if_not_exists => TRUE
);
