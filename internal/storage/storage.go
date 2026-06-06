package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sample is one telemetry point at a timestamp.
type Sample struct {
	Time           time.Time
	OrganizationID string
	MetricKey      string
	Value          float64
	Labels         map[string]string
}

// Open creates a connection pool.
func Open(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	applyPoolTuning(cfg)
	return pgxpool.NewWithConfig(ctx, cfg)
}

// InitSchema creates extension, table, hypertable, and indexes (idempotent).
func InitSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE EXTENSION IF NOT EXISTS timescaledb`,
		`CREATE TABLE IF NOT EXISTS telemetry_samples (
			time timestamptz NOT NULL,
			organization_id text NOT NULL,
			metric_key text NOT NULL,
			value double precision NOT NULL,
			labels jsonb NOT NULL DEFAULT '{}'::jsonb
		)`,
		`CREATE INDEX IF NOT EXISTS telemetry_samples_org_metric_time
			ON telemetry_samples (organization_id, metric_key, time DESC)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: exec schema: %w", err)
		}
	}
	_, err := pool.Exec(ctx, `SELECT create_hypertable('telemetry_samples', 'time', if_not_exists => TRUE)`)
	if err != nil {
		// Older TimescaleDB without if_not_exists — treat "already a hypertable" as OK.
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "already a hypertable") || strings.Contains(msg, "is already a hypertable") {
			return nil
		}
		return fmt.Errorf("storage: create_hypertable: %w", err)
	}
	return nil
}

// InsertSamples writes a batch using CopyFrom.
func InsertSamples(ctx context.Context, pool *pgxpool.Pool, samples []Sample) error {
	if len(samples) == 0 {
		return nil
	}
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	rows, err := toCopyRows(samples)
	if err != nil {
		return err
	}
	_, err = pool.CopyFrom(
		ctx,
		pgx.Identifier{"telemetry_samples"},
		[]string{"time", "organization_id", "metric_key", "value", "labels"},
		pgx.CopyFromRows(rows),
	)
	return err
}

// DeleteSamplesInRange removes every row of telemetry_samples that
// matches the (organization_id, metric_key ∈ metricKeys, [from, to])
// predicate and returns the number of deleted rows. Used by the
// energy-flow backfill endpoint to make recompute idempotent: dropping
// the previously-emitted synthetic samples in the window before
// inserting the fresh ones avoids two cumulative samples at the same
// timestamp drifting apart on repeated clicks.
//
// The bound is right-closed (time <= to). It mirrors the
// energy-summary lookup so a recompute over [from, to] cleans exactly
// the same range the next summary query will read against.
func DeleteSamplesInRange(
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID string,
	metricKeys []string,
	from, to time.Time,
) (int64, error) {
	if pool == nil {
		return 0, fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return 0, fmt.Errorf("storage: organization_id is required")
	}
	if len(metricKeys) == 0 {
		return 0, fmt.Errorf("storage: metric_keys is required")
	}
	if from.IsZero() || to.IsZero() {
		return 0, fmt.Errorf("storage: from and to are required")
	}
	if !to.After(from) {
		return 0, fmt.Errorf("storage: to must be after from")
	}
	tag, err := pool.Exec(ctx, `
		DELETE FROM telemetry_samples
		WHERE organization_id = $1
			AND metric_key = ANY($2)
			AND time >= $3
			AND time <= $4
	`, organizationID, metricKeys, from.UTC(), to.UTC())
	if err != nil {
		return 0, fmt.Errorf("storage: delete samples in range: %w", err)
	}
	return tag.RowsAffected(), nil
}

// DeleteArchiveSamplesInRange removes only the rows tagged with the
// given source label (labels->>'source' = source) for
// (organization_id, metric_key ∈ metricKeys, [from, to]). Used by the
// FusionSolar importer to make a re-import idempotent WITHOUT ever
// touching live Modbus samples: those carry no `source` label, so a
// re-import deletes and rewrites strictly its own archive rows. The
// source tag also lets an operator wipe + re-pull the archive later.
//
// The bound is right-closed (time <= to) to mirror the query side;
// combined with the source filter, real data is safe even if a
// timestamp coincides.
func DeleteArchiveSamplesInRange(
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID string,
	metricKeys []string,
	source string,
	from, to time.Time,
) (int64, error) {
	if pool == nil {
		return 0, fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return 0, fmt.Errorf("storage: organization_id is required")
	}
	if len(metricKeys) == 0 {
		return 0, fmt.Errorf("storage: metric_keys is required")
	}
	if strings.TrimSpace(source) == "" {
		return 0, fmt.Errorf("storage: source is required")
	}
	if from.IsZero() || to.IsZero() {
		return 0, fmt.Errorf("storage: from and to are required")
	}
	if !to.After(from) {
		return 0, fmt.Errorf("storage: to must be after from")
	}
	tag, err := pool.Exec(ctx, `
		DELETE FROM telemetry_samples
		WHERE organization_id = $1
			AND metric_key = ANY($2)
			AND labels->>'source' = $3
			AND time >= $4
			AND time <= $5
	`, organizationID, metricKeys, source, from.UTC(), to.UTC())
	if err != nil {
		return 0, fmt.Errorf("storage: delete archive samples in range: %w", err)
	}
	return tag.RowsAffected(), nil
}

func toCopyRows(samples []Sample) ([][]any, error) {
	rows := make([][]any, len(samples))
	for i, s := range samples {
		labels := s.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		b, err := json.Marshal(labels)
		if err != nil {
			return nil, err
		}
		rows[i] = []any{s.Time.UTC(), s.OrganizationID, s.MetricKey, s.Value, b}
	}
	return rows, nil
}

func applyPoolTuning(cfg *pgxpool.Config) {
	if cfg.ConnConfig.ConnectTimeout <= 0 {
		cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	}
	cfg.MaxConns = int32(getIntEnv("SESTELEMETRY_DB_MAX_CONNS", 10))
	cfg.MinConns = int32(getIntEnv("SESTELEMETRY_DB_MIN_CONNS", 1))
	cfg.MaxConnIdleTime = getDurationEnv("SESTELEMETRY_DB_MAX_CONN_IDLE_TIME", 5*time.Minute)
	cfg.MaxConnLifetime = getDurationEnv("SESTELEMETRY_DB_MAX_CONN_LIFETIME", 30*time.Minute)
}

func getIntEnv(key string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func getDurationEnv(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fallback
	}
	return d
}
