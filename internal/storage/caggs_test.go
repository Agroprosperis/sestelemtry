package storage

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Every collector start runs InitContinuousAggregates. It must leave
// today's bucket to real-time aggregation: a reading that lands after
// the start still reaches today's totals. When the startup refresh
// materialized today, the day summary stayed at the startup values
// until midnight (ab, 5 Nov 2026: 19.9 kWh of a 1.1 MWh day).
//
// Needs a throwaway TimescaleDB named by TIMESCALE_TEST_DATABASE_URL;
// skipped without it.
func TestInitContinuousAggregatesLeavesTodayLive(t *testing.T) {
	dsn := os.Getenv("TIMESCALE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TIMESCALE_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := InitSchema(ctx, pool); err != nil {
		t.Fatalf("schema: %v", err)
	}
	const org, metric = "cagg-test", "accumulated_pv_energy_yield_kwh"
	if _, err := pool.Exec(ctx, `DELETE FROM telemetry_samples WHERE organization_id = $1`, org); err != nil {
		t.Fatalf("reset: %v", err)
	}
	insert := func(at time.Time, value float64) {
		t.Helper()
		if _, err := pool.Exec(ctx,
			`INSERT INTO telemetry_samples (time, organization_id, metric_key, value) VALUES ($1, $2, $3, $4)`,
			at, org, metric, value); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}

	now := time.Now()
	insert(now.Add(-2*time.Minute), 100)
	if err := InitContinuousAggregates(ctx, pool); err != nil {
		t.Fatalf("init caggs: %v", err)
	}
	insert(now.Add(-time.Minute), 150)

	var last float64
	err = pool.QueryRow(ctx, `
		SELECT last_value FROM `+DailyCAGGView+`
		WHERE organization_id = $1 AND metric_key = $2
		ORDER BY day DESC LIMIT 1`, org, metric).Scan(&last)
	if err != nil {
		t.Fatalf("read today: %v", err)
	}
	if last != 150 {
		t.Fatalf("today's last value = %v, want 150: the startup refresh froze today's bucket", last)
	}
}
