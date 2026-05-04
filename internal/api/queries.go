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

type Store struct {
	pool       *pgxpool.Pool
	useHourCAG bool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// EnableHourlyCAGG marks the hourly continuous aggregate as available so
// that timeseries queries with bucket >= 1 hour read from the materialized
// view instead of scanning the raw hypertable. Callers should invoke
// `storage.HourlyCAGGAvailable` once at boot and pass the result here; if
// the CAGG is later created, a service restart picks it up.
func (s *Store) EnableHourlyCAGG(enabled bool) {
	s.useHourCAG = enabled
}

// bucketAtLeastOneHour returns true when the bucket interval is large
// enough that the hourly CAGG carries enough resolution to answer the
// query. Sub-hour buckets (5/10/15 minutes) must read raw samples to keep
// the day-preset chart granular.
//
// Recognising this in Go avoids a round-trip to PostgreSQL just to compare
// `$1::interval >= INTERVAL '1 hour'`. The set of bucket strings the
// dashboard sends is small and well known; arbitrary user-supplied
// strings (via swagger) gracefully fall back to the raw path.
func bucketAtLeastOneHour(bucket string) bool {
	s := strings.ToLower(strings.TrimSpace(bucket))
	if s == "" {
		return false
	}
	if strings.Contains(s, "hour") ||
		strings.Contains(s, "day") ||
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
	if at.IsZero() {
		rows, err = s.pool.Query(ctx, `
			SELECT DISTINCT ON (metric_key)
				metric_key, value, time, labels
			FROM telemetry_samples
			WHERE organization_id = $1 AND metric_key = ANY($2)
			ORDER BY metric_key, time DESC
		`, organizationID, metricKeys)
	} else {
		rows, err = s.pool.Query(ctx, `
			SELECT DISTINCT ON (metric_key)
				metric_key, value, time, labels
			FROM telemetry_samples
			WHERE organization_id = $1
				AND metric_key = ANY($2)
				AND time <= $3
			ORDER BY metric_key, time DESC
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

	useCAGG := s.useHourCAG && bucketAtLeastOneHour(bucket)

	switch aggregation {
	case AggregationDelta:
		if useCAGG {
			return s.timeseriesDeltaFromHourly(ctx, organizationID, metricKeys, from, to, bucket, tz)
		}
		return s.timeseriesDelta(ctx, organizationID, metricKeys, from, to, bucket, tz)
	case AggregationAvg, AggregationLast:
		if useCAGG {
			return s.timeseriesInstantFromHourly(ctx, organizationID, metricKeys, from, to, bucket, tz, aggregation)
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
		)
		SELECT
			bucket_time,
			metric_key,
			GREATEST(
				COALESCE(
					last_value - lag(last_value) OVER (
						PARTITION BY metric_key ORDER BY bucket_time
					),
					last_value - first_value
				),
				0
			) AS value
		FROM bucketed
		WHERE bucket_time >= $4
		ORDER BY bucket_time ASC, metric_key ASC
	`, bucket, organizationID, metricKeys, from.UTC(), to.UTC(), tz)
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

// timeseriesDeltaFromHourly is the CAGG-backed equivalent of
// timeseriesDelta. It re-buckets the materialized hourly snapshots
// (`last(value, time)` per (org, metric, hour)) into the requested
// `bucket`, then computes per-bucket counter contribution as
// `last - lag(last)` clamped to >= 0, with the same NULL-seed fallback
// to in-bucket `last - first` as the raw path so the very first day of
// a fresh deployment still renders a non-zero bar.
//
// The lookback window is widened by one bucket so the first displayed
// bucket can find a seed in the previous bucket whenever data exists
// there. Real-time aggregation in Timescale fills in any hours newer
// than the refresh watermark, so month/year queries see an up-to-date
// last bucket without us having to touch the raw hypertable.
func (s *Store) timeseriesDeltaFromHourly(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string) (TimeseriesResponse, error) {
	rows, err := s.pool.Query(ctx, `
		WITH bucketed AS (
			SELECT
				time_bucket($1::interval, hour, $6::text) AS bucket_time,
				metric_key,
				first(last_value, hour) AS first_value,
				last(last_value, hour)  AS last_value
			FROM `+storage.HourlyCAGGView+`
			WHERE organization_id = $2
				AND metric_key = ANY($3)
				AND hour >= $4::timestamptz - $1::interval
				AND hour <= $5
			GROUP BY bucket_time, metric_key
		)
		SELECT
			bucket_time,
			metric_key,
			GREATEST(
				COALESCE(
					last_value - lag(last_value) OVER (
						PARTITION BY metric_key ORDER BY bucket_time
					),
					last_value - first_value
				),
				0
			) AS value
		FROM bucketed
		WHERE bucket_time >= $4
		ORDER BY bucket_time ASC, metric_key ASC
	`, bucket, organizationID, metricKeys, from.UTC(), to.UTC(), tz)
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

// timeseriesInstantFromHourly is the CAGG-backed equivalent of
// timeseriesInstant for buckets >= 1 hour. For `avg` we average the
// per-hour means weighted by sample_count to keep the result statistically
// correct when hourly buckets contain different numbers of raw samples.
// For `last` we take the last hourly snapshot inside each bucket.
func (s *Store) timeseriesInstantFromHourly(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string, aggregation TimeseriesAggregation) (TimeseriesResponse, error) {
	var valueExpr string
	switch aggregation {
	case AggregationAvg:
		// Weighted average: Σ(avg_value * sample_count) / Σ(sample_count).
		// This recovers the same answer raw avg(value) would produce.
		valueExpr = "sum(avg_value * sample_count) / NULLIF(sum(sample_count), 0)"
	case AggregationLast:
		valueExpr = "last(last_value, hour)"
	default:
		return TimeseriesResponse{}, fmt.Errorf("unsupported instant aggregation %q", aggregation)
	}

	sql := fmt.Sprintf(`
		SELECT
			time_bucket($1::interval, hour, $6::text) AS bucket_time,
			metric_key,
			%s AS value
		FROM `+storage.HourlyCAGGView+`
		WHERE organization_id = $2
			AND metric_key = ANY($3)
			AND hour >= $4
			AND hour <= $5
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

// EnergySummary returns the period-total contribution of each requested
// accumulator metric, computed as `end - seed`, clamped to >= 0. When
// metricKeys is empty we default to EnergySummaryAccumulators.
//
// `end`  is the latest sample at-or-before `to`.
// `seed` is the latest sample strictly before `from`; when no such sample
//        exists (typical on the first period after a fresh deployment),
//        we fall back to the EARLIEST sample inside [from, to] so the
//        result reflects in-period accumulation rather than the
//        lifetime-cumulative end value. Without this fallback the first
//        month after install reports the whole lifetime counter as the
//        month total — which inflates "produced / consumed" cards by an
//        order of magnitude on a brand-new install.
//
// Three indexed lookups per metric (end, before-seed, in-period-first)
// stay O(log n) regardless of the period length, so monthly/yearly
// summaries no longer require the frontend to sum 30+ bucket deltas.
// `applyApplianceConsumptionRule` runs after the totals are read so the
// algebraic appliance-consumption fallback applies on the server side.
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

	rows, err := s.pool.Query(ctx, `
		SELECT
			m.metric_key,
			(
				SELECT value FROM telemetry_samples
				WHERE organization_id = $1
					AND metric_key = m.metric_key
					AND time <= $4
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
					AND time <= $4
				ORDER BY time ASC
				LIMIT 1
			) AS in_period_first
		FROM unnest($2::text[]) AS m(metric_key)
	`, organizationID, metricKeys, from.UTC(), to.UTC())
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

// applyApplianceConsumptionRule mirrors the frontend rule: when the
// device-reported `accumulated_power_consumption_kwh` counter is missing
// or stuck at zero, derive consumption algebraically from the energy
// balance (purchase + pv + discharge - charge). Some Huawei deployments
// (notably the pe inverter) leave the consumption register at 0 even
// while the household actively consumes energy, which would otherwise
// collapse the dashboard summary to "0 spent".
//
// The rule is identical to the TypeScript implementation in
// `web/src/dashboard/transforms/buckets.ts`. Putting it here on the
// server lets the frontend trust the totals as-is and removes one source
// of discrepancy between the API and the UI.
func applyApplianceConsumptionRule(totals map[string]float64) {
	const consumptionKey = "accumulated_power_consumption_kwh"
	raw, ok := totals[consumptionKey]
	if !ok {
		return
	}
	if raw > 0 {
		return
	}
	value := totals["accumulated_electricity_purchased_kwh"] +
		totals["accumulated_pv_energy_yield_kwh"] +
		totals["total_energy_discharged_kwh"] -
		totals["total_energy_charged_kwh"]
	if value < 0 {
		value = 0
	}
	totals[consumptionKey] = value
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

func (s *Store) Ready(ctx context.Context) error {
	return s.pool.Ping(ctx)
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
