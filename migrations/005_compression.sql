-- Enable TimescaleDB native compression on telemetry_samples, cut it into
-- day-sized chunks, and schedule a background policy that compresses each
-- chunk an hour after its day ends. Also applied programmatically by the
-- collector via storage.InitCompression on every startup, so this file is
-- for documentation and manual replay.
--
-- Rationale: production cadence is 1Hz × ~20 metrics × 8 sites ≈ 14M
-- rows/day ≈ 3.5 GB/day on the raw hypertable including its indexes.
-- Columnar compression on these slowly-changing doubles, segmented by
-- (organization_id, metric_key), reproducibly hits ~25-60× total size
-- reduction (heap collapses under Gorilla + delta-of-delta, the row-level
-- index is replaced by per-segment min/max metadata so its space is
-- recovered too).
--
-- Compressing early is also what keeps reads fast. Raw rows land in
-- arrival order, so an uncompressed chunk interleaves every site's
-- metrics second by second and one site-day read touches nearly every
-- page of that day (seconds, and tens of seconds once off the cache); a
-- compressed day reads only its own (site, metric) segments (~0.4 s for
-- the day chart). Day-sized chunks keep the uncompressed tail — today
-- plus the hour after midnight UTC — small enough to stay in memory. New
-- inserts always land in the active (uncompressed) chunk; late rows still
-- insert into a compressed chunk's uncompressed part, which the policy
-- recompresses. The worker never blocks writes.

ALTER TABLE telemetry_samples SET (
    timescaledb.compress,
    timescaledb.compress_segmentby = 'organization_id, metric_key',
    timescaledb.compress_orderby   = 'time DESC'
);

-- Only shapes chunks created from now on.
SELECT set_chunk_time_interval('telemetry_samples', INTERVAL '1 day');

SELECT add_compression_policy(
    'telemetry_samples',
    INTERVAL '1 hour',
    if_not_exists => TRUE,
    schedule_interval => INTERVAL '1 hour'
);

-- if_not_exists leaves an existing policy as it was; align one created
-- with other settings.
SELECT alter_job(
    job_id,
    schedule_interval => INTERVAL '1 hour',
    config => jsonb_set(config, '{compress_after}', to_jsonb('1 hour'::text))
)
FROM timescaledb_information.jobs
WHERE proc_name = 'policy_compression'
    AND hypertable_name = 'telemetry_samples'
    AND ((config->>'compress_after')::interval IS DISTINCT FROM INTERVAL '1 hour'
        OR schedule_interval IS DISTINCT FROM INTERVAL '1 hour');
