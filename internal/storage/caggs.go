package storage

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DailyCAGGView is the relation name of the daily continuous aggregate
// over telemetry_samples. The API reads this view for any bucket >= 1 day
// (month/year presets) so it doesn't have to scan the raw hypertable.
// Day-preset queries (5-minute buckets) still hit the raw hypertable
// directly because per-day granularity is too coarse for an intra-day
// chart.
//
// The view stores first(value, time), last(value, time), avg(value) and
// count(*), which is everything the API needs for both delta-style chart
// queries (with NULL-seed fallback) and the energy-summary endpoint.
const DailyCAGGView = "telemetry_samples_daily"

// LegacyHourlyCAGGView is the previous, hourly-granularity CAGG that
// migration 003 created. We DROP it on every collector startup so a
// rolling upgrade from 003 → 004 cleans up automatically without
// requiring an operator to run the 004 migration by hand.
const LegacyHourlyCAGGView = "telemetry_samples_hourly"

// caggDayBucketTZ is hard-coded into the view definition because a
// continuous aggregate's time_bucket() arguments are immutable after
// creation. The dashboard renders Ukrainian deployments today, so
// Europe/Kyiv local midnight is the right boundary. If a different
// region ever needs its own dashboard, the right answer is a parallel
// daily CAGG keyed by tz, not a runtime parameter.
const caggDayBucketTZ = "Europe/Kyiv"

// InitContinuousAggregates ensures the daily CAGG and its supporting
// refresh policy + index exist, and drops the legacy hourly CAGG when
// it is still around from migration 003. Idempotent: safe to call on
// every collector startup.
//
// Real-time aggregation stays enabled (default), so reads above the
// refresh watermark fall through to raw data automatically without an
// API-level fallback path.
func InitContinuousAggregates(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}

	// Drop the legacy hourly CAGG first so deployments that already ran
	// migration 003 don't keep two parallel materializations refreshing
	// in the background. CASCADE removes its policy + index in one go.
	if _, err := pool.Exec(ctx, `DROP MATERIALIZED VIEW IF EXISTS `+LegacyHourlyCAGGView+` CASCADE`); err != nil {
		return fmt.Errorf("storage: drop legacy cagg %s: %w", LegacyHourlyCAGGView, err)
	}

	// CREATE MATERIALIZED VIEW ... WITH (timescaledb.continuous) cannot
	// run inside a transaction; pgx.Exec auto-commits each statement.
	const createView = `
		CREATE MATERIALIZED VIEW IF NOT EXISTS ` + DailyCAGGView + `
		WITH (timescaledb.continuous) AS
		SELECT
			time_bucket(INTERVAL '1 day', time, '` + caggDayBucketTZ + `') AS day,
			organization_id,
			metric_key,
			first(value, time) AS first_value,
			last(value, time)  AS last_value,
			avg(value)         AS avg_value,
			count(*)           AS sample_count
		FROM telemetry_samples
		GROUP BY day, organization_id, metric_key
		WITH NO DATA
	`
	if _, err := pool.Exec(ctx, createView); err != nil {
		return fmt.Errorf("storage: create cagg %s: %w", DailyCAGGView, err)
	}

	const policy = `
		SELECT add_continuous_aggregate_policy(
			$1,
			start_offset => NULL,
			end_offset   => INTERVAL '15 minutes',
			schedule_interval => INTERVAL '15 minutes',
			if_not_exists => TRUE
		)
	`
	if _, err := pool.Exec(ctx, policy, DailyCAGGView); err != nil {
		// Some Timescale Cloud tiers reject explicit policies; missing
		// policy doesn't break correctness (queries still work via
		// real-time aggregation) so we swallow "already exists" cases.
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "already exists") {
			return fmt.Errorf("storage: add cagg policy: %w", err)
		}
	}

	const index = `
		CREATE INDEX IF NOT EXISTS telemetry_samples_daily_org_metric_day
			ON ` + DailyCAGGView + ` (organization_id, metric_key, day DESC)
	`
	if _, err := pool.Exec(ctx, index); err != nil {
		return fmt.Errorf("storage: create cagg index: %w", err)
	}

	// Trigger the initial backfill (no-op on subsequent calls). NULL
	// bounds let the policy walk the entire range; it blocks the
	// collector startup briefly on the very first deploy / migration
	// 003 → 004 upgrade.
	if _, err := pool.Exec(ctx, `CALL refresh_continuous_aggregate($1, NULL, NULL)`, DailyCAGGView); err != nil {
		return fmt.Errorf("storage: refresh cagg: %w", err)
	}

	return nil
}

// DailyCAGGAvailable reports whether the daily CAGG view exists and is
// queryable in the connected database. The API uses this at boot to
// pick the fast (CAGG) or fallback (raw hypertable) path for
// monthly/yearly timeseries queries without crashing on a database that
// hasn't run migration 004 yet.
func DailyCAGGAvailable(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
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
	`, DailyCAGGView).Scan(&exists)
	if err != nil {
		// Missing timescaledb_information schema means the extension
		// isn't installed at all — treat as "not available" rather than
		// failing API startup.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "does not exist") {
			return false, nil
		}
		return false, err
	}
	return exists, nil
}
