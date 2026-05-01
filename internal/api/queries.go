package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Current(ctx context.Context, organizationID string, metricKeys []string) (CurrentResponse, error) {
	if len(metricKeys) == 0 {
		metricKeys = DefaultDashboardMetrics
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT ON (metric_key)
			metric_key, value, time, labels
		FROM telemetry_samples
		WHERE organization_id = $1 AND metric_key = ANY($2)
		ORDER BY metric_key, time DESC
	`, organizationID, metricKeys)
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

func (s *Store) Timeseries(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket, tz string) (TimeseriesResponse, error) {
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
	rows, err := s.pool.Query(ctx, `
		WITH bucketed AS (
			SELECT
				time_bucket($1::interval, time, $6::text) AS bucket_time,
				metric_key,
				last(value, time) AS last_value
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
				last_value - lag(last_value) OVER (
					PARTITION BY metric_key ORDER BY bucket_time
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
