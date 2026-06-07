package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InitFusionKpiSchema creates the canonical daily-KPI table (idempotent).
// The FusionSolar importer fills it from getKpiStationDay; the economics
// service reads it to reconcile computed daily totals against the
// FusionSolar UI numbers. Called at API startup like InitEconomicsSchema.
func InitFusionKpiSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	const stmt = `
		CREATE TABLE IF NOT EXISTS fusionsolar_daily_kpi (
			organization_id   text NOT NULL,
			day               date NOT NULL,
			plant_code        text NOT NULL,
			pv_yield_kwh      double precision NOT NULL DEFAULT 0,
			use_power_kwh     double precision NOT NULL DEFAULT 0,
			buy_power_kwh     double precision NOT NULL DEFAULT 0,
			ongrid_power_kwh  double precision NOT NULL DEFAULT 0,
			charge_cap_kwh    double precision NOT NULL DEFAULT 0,
			discharge_cap_kwh double precision NOT NULL DEFAULT 0,
			self_use_power_kwh double precision NOT NULL DEFAULT 0,
			raw               jsonb,
			fetched_at        timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id, day)
		)`
	if _, err := pool.Exec(ctx, stmt); err != nil {
		return fmt.Errorf("storage: exec fusionsolar kpi schema: %w", err)
	}
	return nil
}

// FusionDailyKpiRow is one persisted canonical daily KPI.
type FusionDailyKpiRow struct {
	OrganizationID string
	Day            time.Time
	PlantCode      string
	PVYield        float64
	UsePower       float64
	BuyPower       float64
	OnGridPower    float64
	ChargeCap      float64
	DischargeCap   float64
	SelfUsePower   float64
	Raw            json.RawMessage
}

// UpsertFusionDailyKpi stores one canonical daily KPI, replacing any
// prior row for the same (org, day).
func UpsertFusionDailyKpi(ctx context.Context, pool *pgxpool.Pool, rows []FusionDailyKpiRow) (int, error) {
	if pool == nil {
		return 0, fmt.Errorf("storage: nil pool")
	}
	if len(rows) == 0 {
		return 0, nil
	}
	batch := &pgx.Batch{}
	const stmt = `
		INSERT INTO fusionsolar_daily_kpi (
			organization_id, day, plant_code,
			pv_yield_kwh, use_power_kwh, buy_power_kwh, ongrid_power_kwh,
			charge_cap_kwh, discharge_cap_kwh, self_use_power_kwh, raw, fetched_at
		) VALUES ($1,$2::date,$3,$4,$5,$6,$7,$8,$9,$10,$11::jsonb, now())
		ON CONFLICT (organization_id, day) DO UPDATE SET
			plant_code = EXCLUDED.plant_code,
			pv_yield_kwh = EXCLUDED.pv_yield_kwh,
			use_power_kwh = EXCLUDED.use_power_kwh,
			buy_power_kwh = EXCLUDED.buy_power_kwh,
			ongrid_power_kwh = EXCLUDED.ongrid_power_kwh,
			charge_cap_kwh = EXCLUDED.charge_cap_kwh,
			discharge_cap_kwh = EXCLUDED.discharge_cap_kwh,
			self_use_power_kwh = EXCLUDED.self_use_power_kwh,
			raw = EXCLUDED.raw,
			fetched_at = now()
	`
	for _, r := range rows {
		var raw []byte
		if len(r.Raw) > 0 {
			raw = []byte(r.Raw)
		}
		batch.Queue(stmt,
			r.OrganizationID, r.Day, r.PlantCode,
			r.PVYield, r.UsePower, r.BuyPower, r.OnGridPower,
			r.ChargeCap, r.DischargeCap, r.SelfUsePower, raw,
		)
	}
	br := pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return 0, fmt.Errorf("storage: upsert fusionsolar kpi: %w", err)
		}
	}
	return len(rows), nil
}

// GetFusionDailyKpi returns the canonical daily KPI for (org, day). The
// bool is false when no row exists.
func GetFusionDailyKpi(ctx context.Context, pool *pgxpool.Pool, organizationID string, day time.Time) (FusionDailyKpiRow, bool, error) {
	var r FusionDailyKpiRow
	if pool == nil {
		return r, false, fmt.Errorf("storage: nil pool")
	}
	err := pool.QueryRow(ctx, `
		SELECT organization_id, day, plant_code,
			pv_yield_kwh, use_power_kwh, buy_power_kwh, ongrid_power_kwh,
			charge_cap_kwh, discharge_cap_kwh, self_use_power_kwh
		FROM fusionsolar_daily_kpi
		WHERE organization_id = $1 AND day = $2::date
	`, organizationID, day).Scan(
		&r.OrganizationID, &r.Day, &r.PlantCode,
		&r.PVYield, &r.UsePower, &r.BuyPower, &r.OnGridPower,
		&r.ChargeCap, &r.DischargeCap, &r.SelfUsePower,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return r, false, nil
		}
		return r, false, fmt.Errorf("storage: query fusionsolar kpi: %w", err)
	}
	return r, true, nil
}
