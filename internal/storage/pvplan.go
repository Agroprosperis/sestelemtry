package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// InitPvPlanSchema creates the per-day PV plan cache (idempotent). The
// API process owns the table, so cmd/api/main.go runs this at startup
// alongside the economics bootstrap.
func InitPvPlanSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		// planned_kwh = 0 records that the upstream flow was asked and
		// had no forecast for that day. Keeping the row (rather than
		// leaving a hole) is what stops every dashboard load from
		// re-asking for the same missing history; fetched_at lets the
		// caller retry it on a slow cadence in case the flow backfills.
		`CREATE TABLE IF NOT EXISTS pv_plan_daily (
			organization_id text NOT NULL,
			day             date NOT NULL,
			planned_kwh     double precision NOT NULL,
			fetched_at      timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id, day)
		)`,
	}
	for _, stmt := range stmts {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("storage: init pv plan schema: %w", err)
		}
	}
	return nil
}

// PvPlanDay is one cached site-day of planned generation. PlannedKwh
// of 0 means "asked, upstream had nothing" (see InitPvPlanSchema).
type PvPlanDay struct {
	Day        time.Time
	PlannedKwh float64
	FetchedAt  time.Time
}

// ListPvPlanDays returns the cached plan rows for the inclusive civil
// date span [fromDay, toDay], ordered by day. Days are returned as UTC
// midnight instants (Postgres `date` semantics) — the caller compares
// them by calendar date, not by instant.
func ListPvPlanDays(ctx context.Context, pool *pgxpool.Pool, organizationID string, fromDay, toDay time.Time) ([]PvPlanDay, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return nil, fmt.Errorf("storage: empty organization_id")
	}
	rows, err := pool.Query(ctx, `
		SELECT day, planned_kwh, fetched_at
		FROM pv_plan_daily
		WHERE organization_id = $1 AND day >= $2::date AND day <= $3::date
		ORDER BY day ASC
	`, organizationID, fromDay, toDay)
	if err != nil {
		return nil, fmt.Errorf("storage: query pv plan daily: %w", err)
	}
	defer rows.Close()
	var out []PvPlanDay
	for rows.Next() {
		var r PvPlanDay
		if err := rows.Scan(&r.Day, &r.PlannedKwh, &r.FetchedAt); err != nil {
			return nil, fmt.Errorf("storage: scan pv plan daily: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate pv plan daily: %w", err)
	}
	return out, nil
}

// UpsertPvPlanDays writes the given days for one organization in a
// single round trip, overwriting whatever was cached for them.
func UpsertPvPlanDays(ctx context.Context, pool *pgxpool.Pool, organizationID string, days []PvPlanDay) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return fmt.Errorf("storage: empty organization_id")
	}
	if len(days) == 0 {
		return nil
	}
	dayVals := make([]time.Time, 0, len(days))
	kwhVals := make([]float64, 0, len(days))
	for _, d := range days {
		dayVals = append(dayVals, d.Day)
		kwhVals = append(kwhVals, d.PlannedKwh)
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO pv_plan_daily (organization_id, day, planned_kwh, fetched_at)
		SELECT $1, d, k, now()
		FROM unnest($2::date[], $3::double precision[]) AS t(d, k)
		ON CONFLICT (organization_id, day) DO UPDATE
		SET planned_kwh = EXCLUDED.planned_kwh,
		    fetched_at  = EXCLUDED.fetched_at
	`, organizationID, dayVals, kwhVals)
	if err != nil {
		return fmt.Errorf("storage: upsert pv plan daily: %w", err)
	}
	return nil
}
