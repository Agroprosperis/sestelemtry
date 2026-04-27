package storage

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestInsertSamplesEmptyNoPoolNeeded(t *testing.T) {
	if err := InsertSamples(context.Background(), nil, nil); err != nil {
		t.Fatalf("expected nil for empty samples, got %v", err)
	}
}

func TestInsertSamplesRequiresPoolForNonEmptyBatch(t *testing.T) {
	err := InsertSamples(context.Background(), nil, []Sample{{MetricKey: "soc_percent"}})
	if err == nil {
		t.Fatal("expected error for nil pool with non-empty samples")
	}
}

func TestInitSchemaRequiresPool(t *testing.T) {
	err := InitSchema(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil pool")
	}
}

func TestOpenRejectsInvalidURL(t *testing.T) {
	if _, err := Open(context.Background(), "://bad-url"); err == nil {
		t.Fatal("expected parse error for invalid database URL")
	}
}

func TestToCopyRowsNormalizesLabelsAndUTC(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Helsinki")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	inputTime := time.Date(2026, 4, 27, 12, 30, 0, 0, loc)
	rows, err := toCopyRows([]Sample{
		{
			Time:           inputTime,
			OrganizationID: "org-a",
			MetricKey:      "soc_percent",
			Value:          86.5,
			Labels:         nil,
		},
	})
	if err != nil {
		t.Fatalf("toCopyRows error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	ts, ok := rows[0][0].(time.Time)
	if !ok {
		t.Fatalf("expected time.Time, got %T", rows[0][0])
	}
	if ts.Location() != time.UTC {
		t.Fatalf("expected UTC timestamp, got %s", ts.Location())
	}
	rawLabels, ok := rows[0][4].([]byte)
	if !ok {
		t.Fatalf("expected []byte labels, got %T", rows[0][4])
	}
	var labels map[string]string
	if err := json.Unmarshal(rawLabels, &labels); err != nil {
		t.Fatalf("unmarshal labels: %v", err)
	}
	if len(labels) != 0 {
		t.Fatalf("expected empty labels map, got %+v", labels)
	}
}

func TestPoolTuningEnvFallbacks(t *testing.T) {
	t.Setenv("SESTELEMETRY_DB_MAX_CONNS", "not-a-number")
	t.Setenv("SESTELEMETRY_DB_MIN_CONNS", "-1")
	t.Setenv("SESTELEMETRY_DB_MAX_CONN_IDLE_TIME", "bad-duration")
	t.Setenv("SESTELEMETRY_DB_MAX_CONN_LIFETIME", "0s")
	if got := getIntEnv("SESTELEMETRY_DB_MAX_CONNS", 10); got != 10 {
		t.Fatalf("expected fallback max conns, got %d", got)
	}
	if got := getIntEnv("SESTELEMETRY_DB_MIN_CONNS", 1); got != 1 {
		t.Fatalf("expected fallback min conns, got %d", got)
	}
	if got := getDurationEnv("SESTELEMETRY_DB_MAX_CONN_IDLE_TIME", 5*time.Minute); got != 5*time.Minute {
		t.Fatalf("expected fallback idle time, got %s", got)
	}
	if got := getDurationEnv("SESTELEMETRY_DB_MAX_CONN_LIFETIME", 30*time.Minute); got != 30*time.Minute {
		t.Fatalf("expected fallback lifetime, got %s", got)
	}
}

func TestPoolTuningEnvOverrides(t *testing.T) {
	t.Setenv("SESTELEMETRY_DB_MAX_CONNS", "20")
	t.Setenv("SESTELEMETRY_DB_MIN_CONNS", "2")
	t.Setenv("SESTELEMETRY_DB_MAX_CONN_IDLE_TIME", "1m")
	t.Setenv("SESTELEMETRY_DB_MAX_CONN_LIFETIME", "10m")
	if got := getIntEnv("SESTELEMETRY_DB_MAX_CONNS", 10); got != 20 {
		t.Fatalf("expected env max conns, got %d", got)
	}
	if got := getIntEnv("SESTELEMETRY_DB_MIN_CONNS", 1); got != 2 {
		t.Fatalf("expected env min conns, got %d", got)
	}
	if got := getDurationEnv("SESTELEMETRY_DB_MAX_CONN_IDLE_TIME", 5*time.Minute); got != time.Minute {
		t.Fatalf("expected env idle time, got %s", got)
	}
	if got := getDurationEnv("SESTELEMETRY_DB_MAX_CONN_LIFETIME", 30*time.Minute); got != 10*time.Minute {
		t.Fatalf("expected env lifetime, got %s", got)
	}
}
