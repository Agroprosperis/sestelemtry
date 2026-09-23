package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// chunkInterval is the time span of each new telemetry_samples chunk.
// Rows land in arrival order, so an uncompressed chunk interleaves every
// site's metrics second by second and one site-day read touches nearly
// every page of that day. Day-sized chunks keep the uncompressed tail
// small enough to stay in memory and let each finished day be compressed
// on its own. A change only applies to chunks created afterwards.
const chunkInterval = "1 day"

// compressAfter is the chunk-age threshold used by the policy: chunks
// whose entire time range is older than this get compressed by the
// background worker. A compressed segment holds one (organization_id,
// metric_key) in time order, so a closed day reads a fraction of the
// pages it needs raw (the day chart takes ~0.4 s on a compressed day,
// several seconds on a raw one). The hour covers stragglers around
// midnight UTC; rows arriving later still insert into the chunk's
// uncompressed part, which the policy recompresses.
const compressAfter = "1 hour"

// compressSchedule is how often the policy looks for chunks to compress,
// so a finished day is compressed within about an hour of qualifying.
const compressSchedule = "1 hour"

// compressSegmentBy groups rows inside a chunk into columnar segments.
// `(organization_id, metric_key)` is the natural cardinality boundary
// for telemetry — production has ~7 distinct pairs — so RLE collapses
// those columns to a handful of entries per chunk and the row-level
// index is replaced by per-segment min/max metadata on `time` and
// `value`. This matches the existing
// `telemetry_samples_org_metric_time` index shape so post-compression
// reads keep the same access pattern.
const compressSegmentBy = "organization_id, metric_key"

// compressOrderBy keeps the within-segment row order aligned with the
// dashboard's read pattern (newest first), so `last(value, time)` and
// `ORDER BY time DESC LIMIT 1` walks the start of the segment instead
// of decompressing the whole thing.
const compressOrderBy = "time DESC"

// InitCompression enables TimescaleDB native compression on the
// telemetry_samples hypertable, sets its chunk interval, and schedules a
// background policy that compresses chunks older than `compressAfter`.
// Idempotent: safe to call on every collector startup. Errors are
// returned (not panicked) so the caller can decide whether they are
// fatal — for this deployment the call site treats them as non-fatal
// warnings, matching `InitContinuousAggregates`.
//
// Behaviour:
//
//   - ALTER TABLE ... SET (timescaledb.compress, ...) is replayable.
//     On older Timescale versions that reject a no-op SET we swallow
//     "already" errors so the boot doesn't fail on a healthy DB.
//   - set_chunk_time_interval is replayable and only shapes chunks
//     created from now on.
//   - add_compression_policy(..., if_not_exists => TRUE) is the
//     official idempotent form; on rare cloud tiers without it we
//     also swallow "already exists" to stay portable. It leaves an
//     existing policy untouched, so one created with other settings is
//     then aligned through alter_job.
//
// Once the policy is in place, the active (newest) chunk stays
// uncompressed and absorbs all `CopyFrom` inserts; the worker
// compresses older chunks in the background and never touches the
// write path.
func InitCompression(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}

	alter := fmt.Sprintf(`
		ALTER TABLE telemetry_samples SET (
			timescaledb.compress,
			timescaledb.compress_segmentby = '%s',
			timescaledb.compress_orderby   = '%s'
		)
	`, compressSegmentBy, compressOrderBy)
	if _, err := pool.Exec(ctx, alter); err != nil {
		msg := strings.ToLower(err.Error())
		// Some Timescale versions reject a SET that doesn't change the
		// existing options. Treat "already" as success since the
		// observable state matches what we wanted.
		if !strings.Contains(msg, "already") {
			return fmt.Errorf("storage: enable compression: %w", err)
		}
	}

	chunks := fmt.Sprintf(`SELECT set_chunk_time_interval('telemetry_samples', INTERVAL '%s')`, chunkInterval)
	if _, err := pool.Exec(ctx, chunks); err != nil {
		return fmt.Errorf("storage: set chunk interval: %w", err)
	}

	policy := fmt.Sprintf(`
		SELECT add_compression_policy(
			'telemetry_samples',
			INTERVAL '%s',
			if_not_exists => TRUE,
			schedule_interval => INTERVAL '%s'
		)
	`, compressAfter, compressSchedule)
	if _, err := pool.Exec(ctx, policy); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "already exists") {
			return fmt.Errorf("storage: add compression policy: %w", err)
		}
	}

	align := fmt.Sprintf(`
		SELECT alter_job(
			job_id,
			schedule_interval => INTERVAL '%[2]s',
			config => jsonb_set(config, '{compress_after}', to_jsonb('%[1]s'::text))
		)
		FROM timescaledb_information.jobs
		WHERE proc_name = 'policy_compression'
			AND hypertable_name = 'telemetry_samples'
			AND ((config->>'compress_after')::interval IS DISTINCT FROM INTERVAL '%[1]s'
				OR schedule_interval IS DISTINCT FROM INTERVAL '%[2]s')
	`, compressAfter, compressSchedule)
	if _, err := pool.Exec(ctx, align); err != nil {
		return fmt.Errorf("storage: align compression policy: %w", err)
	}
	return nil
}
