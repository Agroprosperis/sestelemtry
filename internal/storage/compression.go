package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// compressAfter is the chunk-age threshold used by the policy: chunks
// whose entire time range is older than this get compressed by the
// background worker. Seven days keeps the day-preset chart (5-minute
// buckets over the last ~24h) hitting only uncompressed chunks, while
// month/year presets transparently read compressed history.
const compressAfter = "7 days"

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
// telemetry_samples hypertable and schedules a background policy that
// compresses chunks older than `compressAfter`. Idempotent: safe to
// call on every collector startup. Errors are returned (not panicked)
// so the caller can decide whether they are fatal — for this
// deployment the call site treats them as non-fatal warnings,
// matching `InitContinuousAggregates`.
//
// Behaviour:
//
//   - ALTER TABLE ... SET (timescaledb.compress, ...) is replayable.
//     On older Timescale versions that reject a no-op SET we swallow
//     "already" errors so the boot doesn't fail on a healthy DB.
//   - add_compression_policy(..., if_not_exists => TRUE) is the
//     official idempotent form; on rare cloud tiers without it we
//     also swallow "already exists" to stay portable.
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

	policy := fmt.Sprintf(`
		SELECT add_compression_policy(
			'telemetry_samples',
			INTERVAL '%s',
			if_not_exists => TRUE
		)
	`, compressAfter)
	if _, err := pool.Exec(ctx, policy); err != nil {
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "already exists") {
			return fmt.Errorf("storage: add compression policy: %w", err)
		}
	}
	return nil
}
