package storage

import (
	"context"
	"encoding/json"
	"fmt"
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
	return pgxpool.NewWithConfig(ctx, cfg)
}

// InitSchema creates extension, table, hypertable, and indexes (idempotent).
func InitSchema(ctx context.Context, pool *pgxpool.Pool) error {
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
	rows := make([][]any, len(samples))
	for i, s := range samples {
		labels := s.Labels
		if labels == nil {
			labels = map[string]string{}
		}
		b, err := json.Marshal(labels)
		if err != nil {
			return err
		}
		rows[i] = []any{s.Time.UTC(), s.OrganizationID, s.MetricKey, s.Value, b}
	}
	_, err := pool.CopyFrom(
		ctx,
		pgx.Identifier{"telemetry_samples"},
		[]string{"time", "organization_id", "metric_key", "value", "labels"},
		pgx.CopyFromRows(rows),
	)
	return err
}
