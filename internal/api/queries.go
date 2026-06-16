package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/nesh/sestelemetry/internal/storage"
)

// maxCounterDeltaPowerKw bounds the per-bucket growth of a cumulative
// energy counter to a physically plausible average power. A single
// corrupted Modbus reading (e.g. one sample of 28846 kWh between
// neighbours of ~50) or a counter re-base can otherwise inject a
// multi-MWh spike into one bucket. Any cross-bucket delta implying an
// average power above this ceiling — measured against the real elapsed
// time between the two consecutive buckets, so genuine multi-hour gap
// recovery is preserved — is treated as spurious and dropped. 10 MW
// sits far above any real site (elevator BESS/PV are well under 2 MW).
const maxCounterDeltaPowerKw = 10000

type Store struct {
	pool        *pgxpool.Pool
	useDailyCAG bool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// EnableDailyCAGG marks the daily continuous aggregate as available so
// that month/year timeseries queries (bucket >= 1 day) and the energy
// summary endpoint read from the materialized view instead of scanning
// the raw hypertable. Callers should invoke `storage.DailyCAGGAvailable`
// once at boot and pass the result here; if the CAGG is later created,
// a service restart picks it up.
func (s *Store) EnableDailyCAGG(enabled bool) {
	s.useDailyCAG = enabled
}

// bucketAtLeastOneDay returns true when the bucket interval is large
// enough that the daily CAGG carries enough resolution to answer the
// query. Sub-day buckets (5 minutes / 1 hour / etc.) must read raw
// samples — the day-preset chart needs intra-day granularity that the
// daily CAGG flattens away.
//
// Recognising this in Go avoids a round-trip to PostgreSQL just to
// compare `$1::interval >= INTERVAL '1 day'`. The set of bucket strings
// the dashboard sends is small and well known; arbitrary user-supplied
// strings (via swagger) gracefully fall back to the raw path.
func bucketAtLeastOneDay(bucket string) bool {
	s := strings.ToLower(strings.TrimSpace(bucket))
	if s == "" {
		return false
	}
	if strings.Contains(s, "day") ||
		strings.Contains(s, "week") ||
		strings.Contains(s, "month") ||
		strings.Contains(s, "year") {
		return true
	}
	return false
}

// Current returns the latest sample per metric. When `at` is non-zero the
// query is scoped to samples at or before that instant (allowing "snapshot at
// a specific timestamp" lookups); otherwise it returns the very latest sample
// regardless of time.
func (s *Store) Current(ctx context.Context, organizationID string, metricKeys []string, at time.Time) (CurrentResponse, error) {
	if len(metricKeys) == 0 {
		metricKeys = DefaultDashboardMetrics
	}
	var (
		rows pgx.Rows
		err  error
	)
	// Per-metric LATERAL lookup instead of `DISTINCT ON (metric_key)
	// … ORDER BY metric_key, time DESC`.
	//
	// On a multi-gigabyte telemetry hypertable the DISTINCT ON variant
	// fans out into a ChunkAppend + Sort + Unique that touches every
	// chunk holding any of the requested metric_keys before it can
	// emit a single row — even though we only need the freshest
	// sample per metric. Postgres has no native "loose index scan",
	// so query latency grew linearly with retention (multiple seconds
	// at ~8 GB / tens of millions of rows). The dashboard polls
	// /current once a second; each tick was racing the previous one
	// to completion, logging `context canceled` and leaving the live
	// cards blank.
	//
	// The LATERAL form runs a tiny `ORDER BY time DESC LIMIT 1`
	// against the `telemetry_samples_org_metric_time` index (which is
	// `(organization_id, metric_key, time DESC)`) once per metric.
	// That's one index-only chunk seek per metric, no global sort.
	// Latency drops to a few milliseconds and stays flat as the
	// hypertable grows.
	if at.IsZero() {
		rows, err = s.pool.Query(ctx, `
			SELECT
				m.metric_key,
				s.value,
				s.time,
				s.labels
			FROM unnest($2::text[]) AS m(metric_key)
			CROSS JOIN LATERAL (
				SELECT value, time, labels
				FROM telemetry_samples
				WHERE organization_id = $1
					AND metric_key = m.metric_key
				ORDER BY time DESC
				LIMIT 1
			) AS s
		`, organizationID, metricKeys)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT
				m.metric_key,
				s.value,
				s.time,
				s.labels
			FROM unnest($2::text[]) AS m(metric_key)
			CROSS JOIN LATERAL (
				SELECT value, time, labels
				FROM telemetry_samples
				WHERE organization_id = $1
					AND metric_key = m.metric_key
					AND time <= $3
				ORDER BY time DESC
				LIMIT 1
			) AS s
		`, organizationID, metricKeys, at.UTC())
	}
	if err != nil {
		return CurrentResponse{}, err
	}
	defer rows.Close()

	out := CurrentResponse{
		OrganizationID: organizationID,
		Metrics:        make(map[string]CurrentMetric, len(metricKeys)),
	}
	for rows.Next() {
		var m CurrentMetric
		var rawLabels []byte
		if err := rows.Scan(&m.MetricKey, &m.Value, &m.Time, &rawLabels); err != nil {
			return CurrentResponse{}, err
		}
		if len(rawLabels) > 0 {
			if err := json.Unmarshal(rawLabels, &m.Labels); err != nil {
				return CurrentResponse{}, err
			}
		}
		out.Metrics[m.MetricKey] = m
	}
	if err := rows.Err(); err != nil {
		return CurrentResponse{}, err
	}
	return out, nil
}

// TimeseriesAggregation selects how the bucket value is computed.
//
//   - "delta" (default): per-bucket contribution of a monotonic counter
//     calculated as last(value, time) of the current bucket minus last value
//     of the previous bucket (negatives clamped to zero).
//   - "avg":   arithmetic mean of samples in the bucket (instantaneous metric).
//   - "last":  the latest sample in the bucket (instantaneous metric).
type TimeseriesAggregation string

const (
	AggregationDelta TimeseriesAggregation = "delta"
	AggregationAvg   TimeseriesAggregation = "avg"
	AggregationLast  TimeseriesAggregation = "last"
)

func (s *Store) Timeseries(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string, aggregation TimeseriesAggregation) (TimeseriesResponse, error) {
	if len(metricKeys) == 0 {
		return TimeseriesResponse{}, fmt.Errorf("metric_keys is required")
	}
	if from.IsZero() {
		from = time.Now().UTC().Add(-24 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if bucket == "" {
		bucket = "15 minutes"
	}
	if strings.TrimSpace(tz) == "" {
		tz = "UTC"
	}
	if aggregation == "" {
		aggregation = AggregationDelta
	}

	useCAGG := s.useDailyCAG && bucketAtLeastOneDay(bucket)

	switch aggregation {
	case AggregationDelta:
		if useCAGG {
			return s.timeseriesDeltaFromDaily(ctx, organizationID, metricKeys, from, to, bucket, tz)
		}
		return s.timeseriesDelta(ctx, organizationID, metricKeys, from, to, bucket, tz)
	case AggregationAvg, AggregationLast:
		if useCAGG {
			return s.timeseriesInstantFromDaily(ctx, organizationID, metricKeys, from, to, bucket, tz, aggregation)
		}
		return s.timeseriesInstant(ctx, organizationID, metricKeys, from, to, bucket, tz, aggregation)
	default:
		return TimeseriesResponse{}, fmt.Errorf("unsupported aggregation %q", aggregation)
	}
}

// timeseriesDelta computes monotonic-counter deltas per bucket. See package
// docs above for why we compare last-of-current-bucket with last-of-previous.
func (s *Store) timeseriesDelta(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string) (TimeseriesResponse, error) {
	// Bucket boundaries are aligned to the caller's timezone so daily-resetting
	// counters (e.g. pv_energy_yield_day_kwh) reset on a bucket edge rather
	// than mid-bucket.
	//
	// Bucket value is the *accumulated counter delta* for the bucket, computed
	// as `last(value, time)` of the current bucket minus `last(value, time)`
	// of the previous bucket. This is robust when a bucket has only a single
	// sample (or none while an adjacent bucket recovers): with last-minus-first
	// inside a single bucket a sparse bucket would silently render as 0 and
	// "lose" the energy that actually accumulated between samples. By
	// referencing the previous bucket's last value we capture the entire
	// delta across sample gaps, so a transient Modbus/collector outage leaves
	// the first bucket after recovery absorbing the missed energy instead of
	// the chart flatlining to zero.
	//
	// We extend the lookback window by one bucket interval so the very first
	// displayed bucket can find a seed value in the previous bucket. Negative
	// deltas (possible on daily-resetting counters or rare manual adjustments)
	// are clamped to zero with GREATEST.
	//
	// When the previous bucket is missing entirely (typical on the very
	// first day of a fresh deployment — no samples exist before the
	// period start), `lag()` returns NULL and the cross-bucket delta is
	// undefined. In that case we fall back to the in-bucket increase
	// `last - first`, which captures the partial-day delta from the
	// first sample of the bucket to the last sample. The bucket then
	// renders the energy actually recorded that day instead of an
	// invisible zero bar.
	rows, err := s.pool.Query(ctx, `
		WITH bucketed AS (
			SELECT
				time_bucket($1::interval, time, $6::text) AS bucket_time,
				metric_key,
				first(value, time) AS first_value,
				last(value, time)  AS last_value
			FROM telemetry_samples
			WHERE organization_id = $2
				AND metric_key = ANY($3)
				AND time >= $4::timestamptz - $1::interval
				AND time <= $5
			GROUP BY bucket_time, metric_key
		),
		deltas AS (
			SELECT
				bucket_time,
				metric_key,
				first_value,
				last_value,
				lag(last_value)  OVER w AS prev_last,
				lag(bucket_time) OVER w AS prev_bucket
			FROM bucketed
			WINDOW w AS (PARTITION BY metric_key ORDER BY bucket_time)
		)
		SELECT
			bucket_time,
			metric_key,
			CASE
				WHEN prev_last IS NOT NULL
					AND (last_value - prev_last) > $7::float8
						* EXTRACT(epoch FROM (bucket_time - prev_bucket)) / 3600.0
				THEN 0
				ELSE GREATEST(
					COALESCE(last_value - prev_last, last_value - first_value),
					0
				)
			END AS value
		FROM deltas
		WHERE bucket_time >= $4
		ORDER BY bucket_time ASC, metric_key ASC
	`, bucket, organizationID, metricKeys, from.UTC(), to.UTC(), tz, float64(maxCounterDeltaPowerKw))
	if err != nil {
		return TimeseriesResponse{}, err
	}
	defer rows.Close()

	out := TimeseriesResponse{
		OrganizationID: organizationID,
		MetricKeys:     metricKeys,
		Bucket:         bucket,
		From:           from.UTC(),
		To:             to.UTC(),
		Points:         make([]TimeseriesPoint, 0),
	}
	for rows.Next() {
		var p TimeseriesPoint
		if err := rows.Scan(&p.Time, &p.MetricKey, &p.Value); err != nil {
			return TimeseriesResponse{}, err
		}
		out.Points = append(out.Points, p)
	}
	if err := rows.Err(); err != nil {
		return TimeseriesResponse{}, err
	}
	return out, nil
}

// timeseriesDeltaFromDaily is the CAGG-backed equivalent of
// timeseriesDelta for buckets >= 1 day. It re-buckets the daily
// snapshots (`last(value, time)` per (org, metric, day) with day
// boundaries aligned to the CAGG's hard-coded local timezone) into the
// requested `bucket`, then computes per-bucket counter contribution as
// `last - lag(last)` clamped to >= 0, with the same NULL-seed fallback
// to in-bucket `last - first` as the raw path so the very first day of
// a fresh deployment still renders a non-zero bar.
//
// The lookback window is widened by one bucket so the first displayed
// bucket can find a seed in the previous bucket whenever data exists
// there. Real-time aggregation in Timescale fills in any days newer
// than the refresh watermark, so month/year queries see an up-to-date
// last bucket without us having to touch the raw hypertable.
//
// For monthly queries the CAGG has at most 31 rows per metric; for
// yearly queries (bucket = 1 month over 12 months) it has at most 366
// rows per metric. Either way the planner walks the (org, metric, day
// DESC) index and scans no raw chunks.
func (s *Store) timeseriesDeltaFromDaily(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string) (TimeseriesResponse, error) {
	rows, err := s.pool.Query(ctx, `
		WITH bucketed AS (
			SELECT
				time_bucket($1::interval, day, $6::text) AS bucket_time,
				metric_key,
				first(first_value, day) AS first_value,
				last(last_value, day)   AS last_value
			FROM `+storage.DailyCAGGView+`
			WHERE organization_id = $2
				AND metric_key = ANY($3)
				AND day >= $4::timestamptz - $1::interval
				AND day <= $5
			GROUP BY bucket_time, metric_key
		),
		deltas AS (
			SELECT
				bucket_time,
				metric_key,
				first_value,
				last_value,
				lag(last_value)  OVER w AS prev_last,
				lag(bucket_time) OVER w AS prev_bucket
			FROM bucketed
			WINDOW w AS (PARTITION BY metric_key ORDER BY bucket_time)
		)
		SELECT
			bucket_time,
			metric_key,
			CASE
				WHEN prev_last IS NOT NULL
					AND (last_value - prev_last) > $7::float8
						* EXTRACT(epoch FROM (bucket_time - prev_bucket)) / 3600.0
				THEN 0
				ELSE GREATEST(
					COALESCE(last_value - prev_last, last_value - first_value),
					0
				)
			END AS value
		FROM deltas
		WHERE bucket_time >= $4
		ORDER BY bucket_time ASC, metric_key ASC
	`, bucket, organizationID, metricKeys, from.UTC(), to.UTC(), tz, float64(maxCounterDeltaPowerKw))
	if err != nil {
		return TimeseriesResponse{}, err
	}
	defer rows.Close()

	out := TimeseriesResponse{
		OrganizationID: organizationID,
		MetricKeys:     metricKeys,
		Bucket:         bucket,
		From:           from.UTC(),
		To:             to.UTC(),
		Points:         make([]TimeseriesPoint, 0),
	}
	for rows.Next() {
		var p TimeseriesPoint
		if err := rows.Scan(&p.Time, &p.MetricKey, &p.Value); err != nil {
			return TimeseriesResponse{}, err
		}
		out.Points = append(out.Points, p)
	}
	if err := rows.Err(); err != nil {
		return TimeseriesResponse{}, err
	}
	return out, nil
}

// timeseriesInstantFromDaily is the CAGG-backed equivalent of
// timeseriesInstant for buckets >= 1 day. For `avg` we average the
// per-day means weighted by sample_count to keep the result
// statistically correct when daily buckets contain different numbers of
// raw samples (e.g. a partial first day of operation). For `last` we
// take the last daily snapshot inside each bucket.
//
// In practice the dashboard never asks for daily-bucketed avg/last —
// SOC and power are intra-day metrics — but the path is symmetric with
// the delta path so any future caller (e.g. an API consumer) gets the
// same fast read.
func (s *Store) timeseriesInstantFromDaily(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string, aggregation TimeseriesAggregation) (TimeseriesResponse, error) {
	var valueExpr string
	switch aggregation {
	case AggregationAvg:
		// Weighted average: Σ(avg_value * sample_count) / Σ(sample_count).
		// This recovers the same answer raw avg(value) would produce.
		valueExpr = "sum(avg_value * sample_count) / NULLIF(sum(sample_count), 0)"
	case AggregationLast:
		valueExpr = "last(last_value, day)"
	default:
		return TimeseriesResponse{}, fmt.Errorf("unsupported instant aggregation %q", aggregation)
	}

	sql := fmt.Sprintf(`
		SELECT
			time_bucket($1::interval, day, $6::text) AS bucket_time,
			metric_key,
			%s AS value
		FROM `+storage.DailyCAGGView+`
		WHERE organization_id = $2
			AND metric_key = ANY($3)
			AND day >= $4
			AND day <= $5
		GROUP BY bucket_time, metric_key
		ORDER BY bucket_time ASC, metric_key ASC
	`, valueExpr)
	rows, err := s.pool.Query(ctx, sql, bucket, organizationID, metricKeys, from.UTC(), to.UTC(), tz)
	if err != nil {
		return TimeseriesResponse{}, err
	}
	defer rows.Close()

	out := TimeseriesResponse{
		OrganizationID: organizationID,
		MetricKeys:     metricKeys,
		Bucket:         bucket,
		From:           from.UTC(),
		To:             to.UTC(),
		Points:         make([]TimeseriesPoint, 0),
	}
	for rows.Next() {
		var p TimeseriesPoint
		if err := rows.Scan(&p.Time, &p.MetricKey, &p.Value); err != nil {
			return TimeseriesResponse{}, err
		}
		out.Points = append(out.Points, p)
	}
	if err := rows.Err(); err != nil {
		return TimeseriesResponse{}, err
	}
	return out, nil
}

// timeseriesInstant returns per-bucket values for instantaneous metrics (SOC,
// power readings, etc.) — either the mean of samples inside the bucket
// ("avg") or the last sample ("last"). No cross-bucket lookback is needed
// because there is no counter delta to compute.
func (s *Store) timeseriesInstant(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string, aggregation TimeseriesAggregation) (TimeseriesResponse, error) {
	var valueExpr string
	switch aggregation {
	case AggregationAvg:
		valueExpr = "avg(value)"
	case AggregationLast:
		valueExpr = "last(value, time)"
	default:
		return TimeseriesResponse{}, fmt.Errorf("unsupported instant aggregation %q", aggregation)
	}

	sql := fmt.Sprintf(`
		SELECT
			time_bucket($1::interval, time, $6::text) AS bucket_time,
			metric_key,
			%s AS value
		FROM telemetry_samples
		WHERE organization_id = $2
			AND metric_key = ANY($3)
			AND time >= $4
			AND time <= $5
		GROUP BY bucket_time, metric_key
		ORDER BY bucket_time ASC, metric_key ASC
	`, valueExpr)
	rows, err := s.pool.Query(ctx, sql, bucket, organizationID, metricKeys, from.UTC(), to.UTC(), tz)
	if err != nil {
		return TimeseriesResponse{}, err
	}
	defer rows.Close()

	out := TimeseriesResponse{
		OrganizationID: organizationID,
		MetricKeys:     metricKeys,
		Bucket:         bucket,
		From:           from.UTC(),
		To:             to.UTC(),
		Points:         make([]TimeseriesPoint, 0),
	}
	for rows.Next() {
		var p TimeseriesPoint
		if err := rows.Scan(&p.Time, &p.MetricKey, &p.Value); err != nil {
			return TimeseriesResponse{}, err
		}
		out.Points = append(out.Points, p)
	}
	if err := rows.Err(); err != nil {
		return TimeseriesResponse{}, err
	}
	return out, nil
}

// summarySQLRaw is the raw-hypertable variant of the EnergySummary
// query: three indexed `LIMIT 1` lookups per metric (end value,
// pre-period seed, in-period-first fallback) served by the
// (organization_id, metric_key, time DESC) index. Each lookup is
// O(log n) regardless of period length.
//
// Seed precedence in the Go scan:
//
//   - `before_seed` — last sample strictly before `from`. This is
//     the natural choice for an established deployment.
//   - `in_period_first` — earliest sample inside [from, to].
//     Falling back here keeps a brand-new install from reporting the
//     entire lifetime counter as the period total: when no pre-period
//     sample exists we treat the period's first sample as the seed
//     so the cards show in-period accumulation.
//
// `end - seed` is clamped at zero. When an inverter glitch rewrites
// the counter mid-period the result will clamp; the dashboard then
// refuses to fabricate a number rather than guessing across the
// rollback.
//
// The half-open semantic [from, to) matters: callers pass `to` as
// "first instant of the next period" (e.g. start of next day for the
// day preset). Using `time <= $4` would pull in the boundary sample
// that already belongs to the next period — a one-second drift on
// the raw path, but on the CAGG path the bucket key `day` is exactly
// that boundary, so `<=` jumps a whole day forward and end−seed
// silently aggregates two days of accumulation. We use `time < $4`
// here so end_value never crosses the period boundary.
const summarySQLRaw = `
	SELECT
		m.metric_key,
		(
			SELECT value FROM telemetry_samples
			WHERE organization_id = $1
				AND metric_key = m.metric_key
				AND time < $4
			ORDER BY time DESC
			LIMIT 1
		) AS end_value,
		(
			SELECT value FROM telemetry_samples
			WHERE organization_id = $1
				AND metric_key = m.metric_key
				AND time < $3
			ORDER BY time DESC
			LIMIT 1
		) AS before_seed,
		(
			SELECT value FROM telemetry_samples
			WHERE organization_id = $1
				AND metric_key = m.metric_key
				AND time >= $3
				AND time < $4
			ORDER BY time ASC
			LIMIT 1
		) AS in_period_first
	FROM unnest($2::text[]) AS m(metric_key)
`

// summarySQLDaily is the daily-CAGG version of the same three
// lookups. Each one walks at most ~30-365 rows on the
// (organization_id, metric_key, day DESC) index; real-time
// aggregation keeps the end_value fresh for the current partial day.
//
// Same half-open semantic as summarySQLRaw: `day < $4`, not `<=`.
// CAGG bucket keys are aligned to local-midnight (Europe/Kyiv) and
// the dashboard's `to` is exactly the next midnight, so `<=` would
// pick the bucket for the day AFTER the requested period and quietly
// double the period's accumulators.
var summarySQLDaily = `
	SELECT
		m.metric_key,
		(
			SELECT last_value FROM ` + storage.DailyCAGGView + `
			WHERE organization_id = $1
				AND metric_key = m.metric_key
				AND day < $4
			ORDER BY day DESC
			LIMIT 1
		) AS end_value,
		(
			SELECT last_value FROM ` + storage.DailyCAGGView + `
			WHERE organization_id = $1
				AND metric_key = m.metric_key
				AND day < $3
			ORDER BY day DESC
			LIMIT 1
		) AS before_seed,
		(
			SELECT first_value FROM ` + storage.DailyCAGGView + `
			WHERE organization_id = $1
				AND metric_key = m.metric_key
				AND day >= $3
				AND day < $4
			ORDER BY day ASC
			LIMIT 1
		) AS in_period_first
	FROM unnest($2::text[]) AS m(metric_key)
`

// EnergySummary returns the period-total contribution of each
// requested accumulator metric, computed as `end - seed` (clamped at
// zero). When metricKeys is empty we default to
// EnergySummaryAccumulators. `applyApplianceConsumptionRule` runs
// after the totals are read so the algebraic appliance-consumption
// fallback applies identically on both the raw and CAGG paths.
//
// All accumulators currently use the same end-minus-seed lookup,
// because that is the right answer for a healthy monotonic counter
// and preserves the constant-time read regardless of period length.
// A counter that rolls back mid-period (firmware bug, manual reset
// on the inverter) will clamp the result to zero — the dashboard
// flags this as "no usable data" rather than reconstructing a
// plausible number from per-day deltas. Recovering from such a
// rollback is a data-quality job (fix the bogus samples in the
// hypertable), not a query-shape one.
func (s *Store) EnergySummary(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time) (EnergySummaryResponse, error) {
	if len(metricKeys) == 0 {
		metricKeys = EnergySummaryAccumulators
	}
	if from.IsZero() || to.IsZero() {
		return EnergySummaryResponse{}, fmt.Errorf("from and to are required")
	}
	if !to.After(from) {
		return EnergySummaryResponse{}, fmt.Errorf("to must be after from")
	}

	sql := summarySQLRaw
	if s.useDailyCAG {
		sql = summarySQLDaily
	}
	rows, err := s.pool.Query(ctx, sql, organizationID, metricKeys, from.UTC(), to.UTC())
	if err != nil {
		return EnergySummaryResponse{}, err
	}
	defer rows.Close()

	out := EnergySummaryResponse{
		OrganizationID: organizationID,
		From:           from.UTC(),
		To:             to.UTC(),
		Totals:         make(map[string]float64, len(metricKeys)),
	}
	for rows.Next() {
		var key string
		var endValue, beforeSeed, inPeriodFirst *float64
		if err := rows.Scan(&key, &endValue, &beforeSeed, &inPeriodFirst); err != nil {
			return EnergySummaryResponse{}, err
		}
		if endValue == nil {
			out.Totals[key] = 0
			continue
		}
		var seed float64
		switch {
		case beforeSeed != nil:
			seed = *beforeSeed
		case inPeriodFirst != nil:
			seed = *inPeriodFirst
		default:
			out.Totals[key] = 0
			continue
		}
		delta := *endValue - seed
		if delta < 0 {
			delta = 0
		}
		out.Totals[key] = delta
	}
	if err := rows.Err(); err != nil {
		return EnergySummaryResponse{}, err
	}
	for _, k := range metricKeys {
		if _, ok := out.Totals[k]; !ok {
			out.Totals[k] = 0
		}
	}

	applyApplianceConsumptionRule(out.Totals)
	return out, nil
}

// applyApplianceConsumptionRule replaces the device-reported
// `accumulated_power_consumption_kwh` counter with the algebraic
// energy-balance identity:
//
//	consumption = pv + purchased + discharge - charge - sold
//
// This is the bus-balance form of Kirchhoff's current law applied
// across a calendar period: the load is whatever was generated /
// imported / discharged minus whatever was stored / exported. The
// rule is unconditional now (was previously a "fallback when counter
// is zero") because the SmartLogger's 40496/98 register tracks only
// the inverter's "Backup load" branch on PE/ZE deployments and
// chronically undercounts real site consumption — the same problem
// that made the dashboard derive the day-chart load line from
// PV+Grid+ESS instead of the raw 40503 register. Trusting the
// counter and trusting the balance disagreed; the balance is
// provably correct by energy conservation, so we standardize on it.
//
// Mirrors the TypeScript implementation in
// `web/src/dashboard/transforms/buckets.ts`.
func applyApplianceConsumptionRule(totals map[string]float64) {
	const consumptionKey = "accumulated_power_consumption_kwh"
	if _, ok := totals[consumptionKey]; !ok {
		return
	}
	value := totals["accumulated_pv_energy_yield_kwh"] +
		totals["accumulated_electricity_purchased_kwh"] +
		totals["total_energy_discharged_kwh"] -
		totals["total_energy_charged_kwh"] -
		totals["accumulated_electricity_sold_kwh"]
	if value < 0 {
		value = 0
	}
	totals[consumptionKey] = value
}

// WeatherForecast returns the cached hourly + daily Open-Meteo forecast
// for the given organization in [from, to]. The bounds are inclusive on
// both ends, mirroring the DAMPrices semantics so the dashboard can
// request `today..today+2d` and receive whatever the weather-collector
// has cached for that window.
//
// Both arrays are always non-nil; empty arrays mean the collector
// hasn't populated this org / range yet, which the frontend treats as
// "fall back to Open-Meteo directly".
func (s *Store) WeatherForecast(
	ctx context.Context,
	organizationID string,
	from, to time.Time,
) (WeatherForecastResponse, error) {
	out := WeatherForecastResponse{
		OrganizationID: organizationID,
		From:           from.UTC(),
		To:             to.UTC(),
		Hourly:         make([]WeatherForecastHour, 0),
		Daily:          make([]WeatherForecastDay, 0),
	}
	hourly, err := storage.QueryWeatherHourly(ctx, s.pool, organizationID, from, to)
	if err != nil {
		return out, fmt.Errorf("query hourly: %w", err)
	}
	for _, r := range hourly {
		out.Hourly = append(out.Hourly, WeatherForecastHour{
			Hour:           r.Hour,
			Temperature2mC: r.Temperature2mC,
			CloudCoverPct:  r.CloudCoverPct,
			IsDay:          r.IsDay,
			ShortwaveWm2:   r.ShortwaveWm2,
			DirectWm2:      r.DirectWm2,
			DiffuseWm2:     r.DiffuseWm2,
			GtiInstantWm2:  r.GtiInstantWm2,
			FetchedAt:      r.FetchedAt,
		})
	}
	daily, err := storage.QueryWeatherDaily(ctx, s.pool, organizationID, from, to)
	if err != nil {
		return out, fmt.Errorf("query daily: %w", err)
	}
	for _, r := range daily {
		out.Daily = append(out.Daily, WeatherForecastDay{
			Day:                   r.Day,
			Sunrise:               r.Sunrise,
			Sunset:                r.Sunset,
			DaylightDurationS:     r.DaylightDurationS,
			SunshineDurationS:     r.SunshineDurationS,
			ShortwaveRadiationSum: r.ShortwaveRadiationSum,
			FetchedAt:             r.FetchedAt,
		})
	}
	return out, nil
}

// DAMPrices returns hourly Day-Ahead Market rows for the inclusive date range
// [from, to] and the given trading zone. Rows are sorted by delivery_date
// ascending then by hour ascending.
func (s *Store) DAMPrices(ctx context.Context, zone int, from, to time.Time) (DAMPricesResponse, error) {
	out := DAMPricesResponse{
		Zone:   zone,
		From:   from.UTC(),
		To:     to.UTC(),
		Prices: make([]DAMPrice, 0),
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
			delivery_date, hour, zone,
			price_uah_per_mwh,
			sale_volume_mwh, purchase_volume_mwh,
			declared_sale_volume_mwh, declared_purchase_volume_mwh
		FROM market_dam_prices
		WHERE zone = $1
			AND delivery_date >= $2::date
			AND delivery_date <= $3::date
		ORDER BY delivery_date ASC, hour ASC
	`, zone, from, to)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p DAMPrice
		if err := rows.Scan(
			&p.DeliveryDate, &p.Hour, &p.Zone,
			&p.PriceUAHPerMWh,
			&p.SaleVolumeMWh, &p.PurchaseVolumeMWh,
			&p.DeclaredSaleVolumeMWh, &p.DeclaredPurchaseVolumeMWh,
		); err != nil {
			return out, err
		}
		out.Prices = append(out.Prices, p)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	return out, nil
}

// Samples streams raw `telemetry_samples` rows in chronological order
// for one organization, restricted to the requested metric keys and
// the half-open interval [from, to]. The caller's `emit` decides how
// to render each row (the HTTP handler writes them as CSV); returning
// an error from `emit` aborts iteration cleanly without consuming the
// rest of the cursor.
//
// `limit <= 0` streams every row in range (the caller's time window is
// the only bound) and never reports truncation. A positive `limit`
// caps the emitted rows: the query reads `limit + 1` rows so we can
// detect truncation — when the underlying data has more rows than the
// cap, we stop after `limit` and report `truncated = true` so the
// caller can flag the export as partial. We don't run a separate
// `COUNT(*)` because per-poll exports can hit millions of rows on
// production data and an extra scan would double the latency.
//
// Rows are ordered by `metric_key ASC, time ASC` — metric-major, not
// time-major. This matches the (organization_id, metric_key, time DESC)
// index and the compression segment layout (segmentby org+metric_key,
// orderby time), so a multi-metric export streams straight off the
// index with no global Sort. A time-major order (`time, metric_key`)
// instead forces Postgres to materialize and sort the entire range
// before emitting the first row, which on a multi-week × multi-metric
// pull (tens of millions of rows) blows past the statement timeout. The
// dashboard pivots the long stream into a wide, time-sorted CSV
// client-side, so the metric-major transport order is invisible to the
// analyst. Note: with a positive `limit`, truncation now drops the
// tail metrics rather than the tail time range — the dashboard sends no
// limit, so its full-range export is unaffected.
func (s *Store) Samples(
	ctx context.Context,
	organizationID string,
	metricKeys []string,
	from, to time.Time,
	limit int,
	emit func(SampleRow) error,
) (rowsEmitted int, truncated bool, err error) {
	if len(metricKeys) == 0 {
		return 0, false, fmt.Errorf("metric_keys is required")
	}
	if from.IsZero() || to.IsZero() {
		return 0, false, fmt.Errorf("from and to are required")
	}
	if !to.After(from) {
		return 0, false, fmt.Errorf("to must be after from")
	}

	// limit <= 0 means "stream everything in range": the caller's
	// time window is the only bound. We drop the LIMIT clause entirely
	// rather than passing a huge sentinel so the planner doesn't reserve
	// a top-N sort, and truncation can never be reported. A positive
	// limit reads limit+1 rows to detect (and flag) truncation.
	unlimited := limit <= 0
	var rows pgx.Rows
	if unlimited {
		rows, err = s.pool.Query(ctx, `
			SELECT time, metric_key, value, labels
			FROM telemetry_samples
			WHERE organization_id = $1
				AND metric_key = ANY($2)
				AND time >= $3
				AND time <  $4
			ORDER BY metric_key ASC, time ASC
		`, organizationID, metricKeys, from.UTC(), to.UTC())
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT time, metric_key, value, labels
			FROM telemetry_samples
			WHERE organization_id = $1
				AND metric_key = ANY($2)
				AND time >= $3
				AND time <  $4
			ORDER BY metric_key ASC, time ASC
			LIMIT $5
		`, organizationID, metricKeys, from.UTC(), to.UTC(), int64(limit)+1)
	}
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()

	for rows.Next() {
		if !unlimited && rowsEmitted >= limit {
			truncated = true
			break
		}
		var r SampleRow
		var rawLabels []byte
		if err := rows.Scan(&r.Time, &r.MetricKey, &r.Value, &rawLabels); err != nil {
			return rowsEmitted, truncated, err
		}
		if len(rawLabels) > 0 {
			labels := map[string]string{}
			if err := json.Unmarshal(rawLabels, &labels); err != nil {
				return rowsEmitted, truncated, err
			}
			if len(labels) > 0 {
				r.Labels = labels
			}
		}
		if err := emit(r); err != nil {
			return rowsEmitted, truncated, err
		}
		rowsEmitted++
	}
	if err := rows.Err(); err != nil {
		return rowsEmitted, truncated, err
	}
	return rowsEmitted, truncated, nil
}

func (s *Store) Ready(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

// GetOrgTariffs returns the persisted tariff bundle for the given org.
// The bool is false when no row exists (the handler maps that to 404
// so the frontend can fall back to bundled defaults). JSON decoding
// happens here so the storage layer can stay schema-free — the API
// owns the DTO shape.
func (s *Store) GetOrgTariffs(ctx context.Context, organizationID string) (OrgTariffs, bool, error) {
	var out OrgTariffs
	payload, ok, err := storage.GetOrgTariffs(ctx, s.pool, organizationID)
	if err != nil {
		return out, false, err
	}
	if !ok {
		return out, false, nil
	}
	if err := json.Unmarshal(payload, &out); err != nil {
		return out, false, fmt.Errorf("decode org tariffs: %w", err)
	}
	return out, true, nil
}

// UpsertOrgTariffs persists the tariff bundle for the given org. The
// handler validates field shapes before calling — we re-encode here
// so the JSONB column always stores the canonical struct shape (in
// particular, missing fields land as their Go zero value rather than
// being absent on disk).
func (s *Store) UpsertOrgTariffs(ctx context.Context, organizationID string, tariffs OrgTariffs) error {
	payload, err := json.Marshal(tariffs)
	if err != nil {
		return fmt.Errorf("encode org tariffs: %w", err)
	}
	return storage.UpsertOrgTariffs(ctx, s.pool, organizationID, payload)
}

// TariffScheduleVersion is one effective-dated tariff version exposed by
// the schedule endpoints. The bundle shape matches OrgTariffs.
type TariffScheduleVersion struct {
	EffectiveFrom string     `json:"effective_from"`
	Tariffs       OrgTariffs `json:"tariffs"`
}

// GetTariffScheduleVersions returns the org's date-versioned tariffs
// ordered ascending by effective_from (decoding the JSONB blob into the
// canonical OrgTariffs struct).
func (s *Store) GetTariffScheduleVersions(ctx context.Context, organizationID string) ([]TariffScheduleVersion, error) {
	entries, err := storage.GetTariffSchedule(ctx, s.pool, organizationID)
	if err != nil {
		return nil, err
	}
	out := make([]TariffScheduleVersion, 0, len(entries))
	for _, e := range entries {
		var t OrgTariffs
		if err := json.Unmarshal(e.Tariffs, &t); err != nil {
			return nil, fmt.Errorf("decode tariff schedule entry: %w", err)
		}
		out = append(out, TariffScheduleVersion{
			EffectiveFrom: e.EffectiveFrom.Format("2006-01-02"),
			Tariffs:       t,
		})
	}
	return out, nil
}

// UpsertTariffScheduleVersion stores one effective-dated tariff version.
func (s *Store) UpsertTariffScheduleVersion(ctx context.Context, organizationID string, effectiveFrom time.Time, tariffs OrgTariffs) error {
	payload, err := json.Marshal(tariffs)
	if err != nil {
		return fmt.Errorf("encode tariff schedule entry: %w", err)
	}
	return storage.UpsertTariffScheduleEntry(ctx, s.pool, organizationID, effectiveFrom, payload)
}

// DeleteTariffScheduleVersion removes one effective-dated tariff version.
func (s *Store) DeleteTariffScheduleVersion(ctx context.Context, organizationID string, effectiveFrom time.Time) (int64, error) {
	return storage.DeleteTariffScheduleEntry(ctx, s.pool, organizationID, effectiveFrom)
}

// SaveEconomicsHourly persists the per-hour economics rows (upsert by
// org + hour_start).
func (s *Store) SaveEconomicsHourly(ctx context.Context, rows []storage.EconomicsHourlyRow) error {
	return storage.UpsertEconomicsHourly(ctx, s.pool, rows)
}

// SaveEconomicsDaily persists the per-day economics summary.
func (s *Store) SaveEconomicsDaily(ctx context.Context, row storage.EconomicsDailyRow) error {
	return storage.UpsertEconomicsDaily(ctx, s.pool, row)
}

// GetEconomicsHourly returns persisted per-hour rows for [from, to).
func (s *Store) GetEconomicsHourly(ctx context.Context, organizationID string, from, to time.Time) ([]storage.EconomicsHourlyRow, error) {
	return storage.GetEconomicsHourly(ctx, s.pool, organizationID, from, to)
}

// GetEconomicsDaily returns the per-day summary for (org, day).
func (s *Store) GetEconomicsDaily(ctx context.Context, organizationID string, day time.Time) (storage.EconomicsDailyRow, bool, error) {
	return storage.GetEconomicsDaily(ctx, s.pool, organizationID, day)
}

// GetEconomicsDailyRange returns persisted per-day summaries for the
// inclusive civil-date span [from, to], ordered by day ascending.
func (s *Store) GetEconomicsDailyRange(ctx context.Context, organizationID string, from, to time.Time) ([]storage.EconomicsDailyRow, error) {
	return storage.GetEconomicsDailyRange(ctx, s.pool, organizationID, from, to)
}

// GetFusionDailyKpi returns the canonical FusionSolar daily KPI for
// (org, day), used to reconcile computed economics.
func (s *Store) GetFusionDailyKpi(ctx context.Context, organizationID string, day time.Time) (storage.FusionDailyKpiRow, bool, error) {
	return storage.GetFusionDailyKpi(ctx, s.pool, organizationID, day)
}

// EnergyFlowSources streams the source-counter rows the recompute
// pipeline needs for the half-open window [from, to) — `to` itself is
// excluded so a sample landing exactly on the next-day midnight isn't
// attributed to hour 0 of the requested day (matching /samples and the
// energy-summary half-open semantics). The window is padded by
// `lookback` so the very first bucket can find a "previous" snapshot to
// subtract against; without that the first interval of a freshly
// recomputed period would be silently dropped.
//
// The query is a plain index scan rather than a server-side
// time_bucket()+last() aggregation on purpose: a hash aggregate over
// a day's worth of telemetry rows pins a Postgres backend on CPU and
// memory until the entire result is materialised, which starves the
// concurrent /current and /timeseries lookups the dashboard depends
// on. The streaming SELECT keeps Postgres latency-friendly and
// pushes the (cheap) per-minute reduction into energyflow.Recompute,
// where it costs a few milliseconds of Go time.
//
// Rows are ordered by (time ASC, metric_key ASC) so the bucketing
// inside energyflow.Recompute can rely on monotonic input. The
// device_host label is unpacked from the labels JSONB so the
// handler can resolve each row to a configured Role without
// re-querying.
//
// The metric_keys catalogue is fixed (EnergyFlowRecomputeSourceMetrics)
// — accepting an arbitrary list here would let a caller drive the
// allocator with the wrong inputs and produce wildly off totals.
func (s *Store) EnergyFlowSources(
	ctx context.Context,
	organizationID string,
	from, to time.Time,
	lookback time.Duration,
) ([]EnergyFlowRawRow, error) {
	if organizationID == "" {
		return nil, fmt.Errorf("organization_id is required")
	}
	if from.IsZero() || to.IsZero() {
		return nil, fmt.Errorf("from and to are required")
	}
	if !to.After(from) {
		return nil, fmt.Errorf("to must be after from")
	}
	if lookback < 0 {
		lookback = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
			time,
			metric_key,
			value,
			COALESCE(labels->>'device_host', '') AS device_host
		FROM telemetry_samples
		WHERE organization_id = $1
			AND metric_key = ANY($2)
			AND time >= $3
			AND time < $4
		ORDER BY time ASC, metric_key ASC
	`, organizationID, EnergyFlowRecomputeSourceMetrics, from.UTC().Add(-lookback), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("energy_flow sources: %w", err)
	}
	defer rows.Close()

	out := make([]EnergyFlowRawRow, 0, 1024)
	for rows.Next() {
		var r EnergyFlowRawRow
		if err := rows.Scan(&r.Time, &r.MetricKey, &r.Value, &r.DeviceHost); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func ParseCSV(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.Split(input, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v == "" {
			continue
		}
		out = append(out, v)
	}
	return out
}
