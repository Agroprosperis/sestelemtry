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

func (s *Store) Timeseries(ctx context.Context, organizationID string, metricKeys []string, from, to time.Time, bucket string) (TimeseriesResponse, error) {
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

	rows, err := s.pool.Query(ctx, `
		SELECT
			time_bucket($1::interval, time) AS bucket_time,
			metric_key,
			AVG(value) AS value
		FROM telemetry_samples
		WHERE organization_id = $2
			AND metric_key = ANY($3)
			AND time >= $4
			AND time <= $5
		GROUP BY bucket_time, metric_key
		ORDER BY bucket_time ASC, metric_key ASC
	`, bucket, organizationID, metricKeys, from.UTC(), to.UTC())
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
