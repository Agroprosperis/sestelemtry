package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// HourlyCAGGView is the relation name of the hourly continuous aggregate
// over telemetry_samples. The API reads this view for any bucket >= 1 hour
// (month/year presets) so it doesn't have to scan the raw hypertable for
// every dashboard load. Day-preset queries (5-minute buckets) still hit
// the raw hypertable directly.
const HourlyCAGGView = "telemetry_samples_hourly"

// InitContinuousAggregates creates the hourly continuous aggregate and its
// supporting refresh policy + index. Idempotent: safe to call on every
// collector startup.
//
// The CAGG materializes:
//   - last(value, time) per (org, metric, hour) — monotonic counter snapshot
//   - avg(value)        per (org, metric, hour) — instantaneous metric mean
//   - count(*)          per (org, metric, hour) — sample count
//
// Real-time aggregation stays enabled (default), so reads above the
// refresh watermark fall through to raw data automatically without an
// API-level fallback path.
func InitContinuousAggregates(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}

	// CREATE MATERIALIZED VIEW ... WITH (timescaledb.continuous) cannot run
	// inside a transaction. pgx's Exec auto-commits each statement, so we
	// just issue them sequentially.
	const createView = `
		CREATE MATERIALIZED VIEW IF NOT EXISTS ` + HourlyCAGGView + `
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
		WITH NO DATA
	`
	if _, err := pool.Exec(ctx, createView); err != nil {
		// Older Timescale builds may not support every modern keyword; we
		// surface the error so install/update logs are noisy.
		return fmt.Errorf("storage: create cagg %s: %w", HourlyCAGGView, err)
	}

	const policy = `
		SELECT add_continuous_aggregate_policy(
			$1,
			start_offset => NULL,
			end_offset   => INTERVAL '15 minutes',
			schedule_interval => INTERVAL '5 minutes',
			if_not_exists => TRUE
		)
	`
	if _, err := pool.Exec(ctx, policy, HourlyCAGGView); err != nil {
		// Some Timescale Cloud tiers reject explicit policies; missing
		// policy doesn't break correctness (queries will still work via
		// real-time aggregation), so warn-and-continue by swallowing the
		// "already exists" / "policy_already_exists" cases.
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "already exists") {
			return fmt.Errorf("storage: add cagg policy: %w", err)
		}
	}

	const index = `
		CREATE INDEX IF NOT EXISTS telemetry_samples_hourly_org_metric_hour
			ON ` + HourlyCAGGView + ` (organization_id, metric_key, hour DESC)
	`
	if _, err := pool.Exec(ctx, index); err != nil {
		return fmt.Errorf("storage: create cagg index: %w", err)
	}

	// Trigger the initial backfill (no-op once historical buckets are
	// already materialized). We pass NULL bounds so the policy walks the
	// entire range. This blocks the collector startup briefly on the very
	// first deploy; subsequent calls are cheap.
	if _, err := pool.Exec(ctx, `CALL refresh_continuous_aggregate($1, NULL, NULL)`, HourlyCAGGView); err != nil {
		return fmt.Errorf("storage: refresh cagg: %w", err)
	}

	return nil
}

// HourlyCAGGAvailable reports whether the hourly CAGG view exists and is
// queryable in the connected database. The API uses this at boot to pick
// the fast (CAGG) or fallback (raw hypertable) path for monthly/yearly
// timeseries queries without crashing on a database that hasn't run
// migration 003 yet.
func HourlyCAGGAvailable(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	if pool == nil {
		return false, fmt.Errorf("storage: nil pool")
	}
	var exists bool
	err := pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM timescaledb_information.continuous_aggregates
			WHERE view_name = $1
		)
	`, HourlyCAGGView).Scan(&exists)
	if err != nil {
		// Missing timescaledb_information schema means the extension isn't
		// installed at all — treat as "not available" rather than failing
		// API startup.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "does not exist") {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}
