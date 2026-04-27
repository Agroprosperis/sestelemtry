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
