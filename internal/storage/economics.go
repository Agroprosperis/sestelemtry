package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// InitEconomicsSchema creates the date-versioned tariff schedule and
// the per-hour / per-day economics result tables (idempotent). The API
// process owns these tables, so cmd/api/main.go runs this at startup —
// mirroring the organization_tariffs bootstrap so a fresh environment
// boots without an external migration step. The seed INSERT backfills
// the schedule from any legacy single-blob organization_tariffs row so
// existing orgs keep resolving a tariff for every historical day.
func InitEconomicsSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS organization_tariff_schedule (
			organization_id text NOT NULL,
			effective_from  date NOT NULL,
			tariffs         jsonb NOT NULL,
			updated_at      timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id, effective_from)
		)`,
		`INSERT INTO organization_tariff_schedule (organization_id, effective_from, tariffs)
			SELECT organization_id, DATE '1970-01-01', tariffs
			FROM organization_tariffs
			ON CONFLICT (organization_id, effective_from) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS economics_hourly (
			organization_id text NOT NULL,
			hour_start      timestamptz NOT NULL,
			rdn_uah_per_kwh          double precision,
			import_price_uah_per_kwh double precision NOT NULL,
			export_price_uah_per_kwh double precision NOT NULL,
			pv_generation_kwh double precision NOT NULL,
			import_total_kwh  double precision NOT NULL,
			export_total_kwh  double precision NOT NULL,
			load_total_kwh    double precision NOT NULL,
			pv_to_load_kwh    double precision NOT NULL,
			pv_to_grid_kwh    double precision NOT NULL,
			grid_to_load_kwh  double precision NOT NULL,
			pv_to_ess_kwh     double precision NOT NULL,
			grid_to_ess_kwh   double precision NOT NULL,
			ess_to_load_kwh   double precision NOT NULL,
			ess_to_grid_kwh   double precision NOT NULL,
			ess_charged_kwh   double precision NOT NULL,
			ess_discharged_kwh double precision NOT NULL,
			baseline_cost_uah double precision NOT NULL,
			actual_cost_uah   double precision NOT NULL,
			effect_uah        double precision NOT NULL,
			ess_net_uah       double precision NOT NULL,
			ess_remaining_kwh_start        double precision,
			ess_avg_cost_uah_per_kwh_start double precision,
			ess_cost_basis_uah_start       double precision,
			ess_withdrawn_cost_uah         double precision,
			ess_realized_profit_uah        double precision,
			ess_avg_cost_uah_per_kwh_end   double precision,
			ess_cost_basis_uah_end         double precision,
			ess_residual_kwh_end           double precision,
			computed_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id, hour_start)
		)`,
		`CREATE INDEX IF NOT EXISTS economics_hourly_org_time_idx
			ON economics_hourly (organization_id, hour_start)`,
		`CREATE TABLE IF NOT EXISTS economics_daily (
			organization_id text NOT NULL,
			day             date NOT NULL,
			tz              text NOT NULL,
			baseline_cost_uah double precision NOT NULL,
			actual_cost_uah   double precision NOT NULL,
			effect_uah        double precision NOT NULL,
			ess_net_uah       double precision NOT NULL,
			load_kwh          double precision NOT NULL,
			pv_kwh            double precision NOT NULL,
			grid_import_kwh   double precision NOT NULL,
			grid_export_kwh   double precision NOT NULL,
			ess_charged_kwh   double precision NOT NULL,
			ess_discharged_kwh double precision NOT NULL,
			pv_to_load_kwh    double precision NOT NULL,
			pv_to_ess_kwh     double precision NOT NULL,
			pv_to_grid_kwh    double precision NOT NULL,
			grid_to_load_kwh  double precision NOT NULL,
			grid_to_ess_kwh   double precision NOT NULL,
			ess_to_load_kwh   double precision NOT NULL,
			ess_to_grid_kwh   double precision NOT NULL,
			avg_import_price_uah_per_kwh double precision NOT NULL,
			avg_export_price_uah_per_kwh double precision NOT NULL,
			revenue_pv_export_uah  double precision NOT NULL,
			revenue_pv_self_uah    double precision NOT NULL,
			revenue_ess_export_uah double precision NOT NULL,
			revenue_ess_self_uah   double precision NOT NULL,
			revenue_total_uah      double precision NOT NULL,
			expense_grid_charge_uah double precision NOT NULL,
			expense_total_uah      double precision NOT NULL,
			ebitda_uah             double precision NOT NULL,
			ess_withdrawn_cost_uah   double precision NOT NULL,
			ess_realized_profit_uah  double precision NOT NULL,
			ess_degradation_cost_uah double precision NOT NULL,
			ess_avg_cost_basis_uah_per_kwh_eod double precision NOT NULL,
			ess_residual_kwh_eod     double precision NOT NULL,
			ess_cost_basis_uah_eod   double precision NOT NULL,
			hours_with_data    integer NOT NULL,
			hours_missing_price integer NOT NULL,
			skip_diagnostics   text NOT NULL DEFAULT '',
			is_final           boolean NOT NULL DEFAULT false,
			computed_at        timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (organization_id, day)
		)`,
		// Reconciliation columns (added incrementally so existing
		// deployments upgrade in place). reconciled flags that the day's
		// flows were scaled to the canonical FusionSolar daily KPIs;
		// quality_flags / reconciliation record the diagnostics.
		`ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS reconciled boolean NOT NULL DEFAULT false`,
		`ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS quality_flags text[]`,
		`ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS reconciliation jsonb`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: exec economics schema: %w", err)
		}
	}
	return nil
}

// TariffScheduleEntry is one effective-dated tariff version. Tariffs is
// the raw JSON blob (same shape as organization_tariffs) so the storage
// layer stays decoupled from the API DTO.
type TariffScheduleEntry struct {
	EffectiveFrom time.Time       `json:"effective_from"`
	Tariffs       json.RawMessage `json:"tariffs"`
}

// GetTariffSchedule returns every effective-dated tariff row for the org,
// ordered by effective_from ascending. An empty slice (no error) means
// the org has no schedule yet.
func GetTariffSchedule(ctx context.Context, pool *pgxpool.Pool, organizationID string) ([]TariffScheduleEntry, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return nil, fmt.Errorf("storage: empty organization_id")
	}
	rows, err := pool.Query(ctx, `
		SELECT effective_from, tariffs
		FROM organization_tariff_schedule
		WHERE organization_id = $1
		ORDER BY effective_from ASC
	`, organizationID)
	if err != nil {
		return nil, fmt.Errorf("storage: query tariff schedule: %w", err)
	}
	defer rows.Close()
	var out []TariffScheduleEntry
	for rows.Next() {
		var e TariffScheduleEntry
		var payload []byte
		if err := rows.Scan(&e.EffectiveFrom, &payload); err != nil {
			return nil, fmt.Errorf("storage: scan tariff schedule: %w", err)
		}
		e.Tariffs = json.RawMessage(payload)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate tariff schedule: %w", err)
	}
	return out, nil
}

// UpsertTariffScheduleEntry stores one effective-dated tariff version,
// replacing any prior row for the same (org, effective_from).
func UpsertTariffScheduleEntry(ctx context.Context, pool *pgxpool.Pool, organizationID string, effectiveFrom time.Time, payload json.RawMessage) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return fmt.Errorf("storage: empty organization_id")
	}
	if len(payload) == 0 {
		return fmt.Errorf("storage: empty tariffs payload")
	}
	const stmt = `
		INSERT INTO organization_tariff_schedule (organization_id, effective_from, tariffs, updated_at)
		VALUES ($1, $2::date, $3::jsonb, now())
		ON CONFLICT (organization_id, effective_from) DO UPDATE SET
			tariffs    = EXCLUDED.tariffs,
			updated_at = now()
	`
	if _, err := pool.Exec(ctx, stmt, organizationID, effectiveFrom, []byte(payload)); err != nil {
		return fmt.Errorf("storage: upsert tariff schedule: %w", err)
	}
	return nil
}

// DeleteTariffScheduleEntry removes one effective-dated tariff version.
func DeleteTariffScheduleEntry(ctx context.Context, pool *pgxpool.Pool, organizationID string, effectiveFrom time.Time) (int64, error) {
	if pool == nil {
		return 0, fmt.Errorf("storage: nil pool")
	}
	tag, err := pool.Exec(ctx, `
		DELETE FROM organization_tariff_schedule
		WHERE organization_id = $1 AND effective_from = $2::date
	`, organizationID, effectiveFrom)
	if err != nil {
		return 0, fmt.Errorf("storage: delete tariff schedule: %w", err)
	}
	return tag.RowsAffected(), nil
}

// EconomicsHourlyRow is one persisted per-hour economics result. Pointer
// fields are nullable (no RDN price for the hour, missing SOC anchor,
// etc.).
type EconomicsHourlyRow struct {
	OrganizationID string
	HourStart      time.Time

	Rdn         *float64
	ImportPrice float64
	ExportPrice float64

	PVGeneration  float64
	ImportTotal   float64
	ExportTotal   float64
	LoadTotal     float64
	PVToLoad      float64
	PVToGrid      float64
	GridToLoad    float64
	PVToEss       float64
	GridToEss     float64
	EssToLoad     float64
	EssToGrid     float64
	EssCharged    float64
	EssDischarged float64

	BaselineCost float64
	ActualCost   float64
	Effect       float64
	EssNet       float64

	EssRemainingKwhStart *float64
	EssAvgCostStart      *float64
	EssCostBasisStart    *float64
	EssWithdrawnCost     *float64
	EssRealizedProfit    *float64
	EssAvgCostEnd        *float64
	EssCostBasisEnd      *float64
	EssResidualEnd       *float64
}

// UpsertEconomicsHourly replaces the persisted per-hour rows by
// (organization_id, hour_start). A re-store of the same day overwrites
// its own rows in place; rows are never deleted so a partial recompute
// can't strand stale hours (every hour of a day is always rewritten).
func UpsertEconomicsHourly(ctx context.Context, pool *pgxpool.Pool, rows []EconomicsHourlyRow) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	if len(rows) == 0 {
		return nil
	}
	batch := &pgx.Batch{}
	const stmt = `
		INSERT INTO economics_hourly (
			organization_id, hour_start,
			rdn_uah_per_kwh, import_price_uah_per_kwh, export_price_uah_per_kwh,
			pv_generation_kwh, import_total_kwh, export_total_kwh, load_total_kwh,
			pv_to_load_kwh, pv_to_grid_kwh, grid_to_load_kwh,
			pv_to_ess_kwh, grid_to_ess_kwh, ess_to_load_kwh, ess_to_grid_kwh,
			ess_charged_kwh, ess_discharged_kwh,
			baseline_cost_uah, actual_cost_uah, effect_uah, ess_net_uah,
			ess_remaining_kwh_start, ess_avg_cost_uah_per_kwh_start, ess_cost_basis_uah_start,
			ess_withdrawn_cost_uah, ess_realized_profit_uah,
			ess_avg_cost_uah_per_kwh_end, ess_cost_basis_uah_end, ess_residual_kwh_end,
			computed_at
		) VALUES (
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,
			$19,$20,$21,$22,$23,$24,$25,$26,$27,$28,$29,$30, now()
		)
		ON CONFLICT (organization_id, hour_start) DO UPDATE SET
			rdn_uah_per_kwh = EXCLUDED.rdn_uah_per_kwh,
			import_price_uah_per_kwh = EXCLUDED.import_price_uah_per_kwh,
			export_price_uah_per_kwh = EXCLUDED.export_price_uah_per_kwh,
			pv_generation_kwh = EXCLUDED.pv_generation_kwh,
			import_total_kwh = EXCLUDED.import_total_kwh,
			export_total_kwh = EXCLUDED.export_total_kwh,
			load_total_kwh = EXCLUDED.load_total_kwh,
			pv_to_load_kwh = EXCLUDED.pv_to_load_kwh,
			pv_to_grid_kwh = EXCLUDED.pv_to_grid_kwh,
			grid_to_load_kwh = EXCLUDED.grid_to_load_kwh,
			pv_to_ess_kwh = EXCLUDED.pv_to_ess_kwh,
			grid_to_ess_kwh = EXCLUDED.grid_to_ess_kwh,
			ess_to_load_kwh = EXCLUDED.ess_to_load_kwh,
			ess_to_grid_kwh = EXCLUDED.ess_to_grid_kwh,
			ess_charged_kwh = EXCLUDED.ess_charged_kwh,
			ess_discharged_kwh = EXCLUDED.ess_discharged_kwh,
			baseline_cost_uah = EXCLUDED.baseline_cost_uah,
			actual_cost_uah = EXCLUDED.actual_cost_uah,
			effect_uah = EXCLUDED.effect_uah,
			ess_net_uah = EXCLUDED.ess_net_uah,
			ess_remaining_kwh_start = EXCLUDED.ess_remaining_kwh_start,
			ess_avg_cost_uah_per_kwh_start = EXCLUDED.ess_avg_cost_uah_per_kwh_start,
			ess_cost_basis_uah_start = EXCLUDED.ess_cost_basis_uah_start,
			ess_withdrawn_cost_uah = EXCLUDED.ess_withdrawn_cost_uah,
			ess_realized_profit_uah = EXCLUDED.ess_realized_profit_uah,
			ess_avg_cost_uah_per_kwh_end = EXCLUDED.ess_avg_cost_uah_per_kwh_end,
			ess_cost_basis_uah_end = EXCLUDED.ess_cost_basis_uah_end,
			ess_residual_kwh_end = EXCLUDED.ess_residual_kwh_end,
			computed_at = now()
	`
	for _, r := range rows {
		batch.Queue(stmt,
			r.OrganizationID, r.HourStart.UTC(),
			r.Rdn, r.ImportPrice, r.ExportPrice,
			r.PVGeneration, r.ImportTotal, r.ExportTotal, r.LoadTotal,
			r.PVToLoad, r.PVToGrid, r.GridToLoad,
			r.PVToEss, r.GridToEss, r.EssToLoad, r.EssToGrid,
			r.EssCharged, r.EssDischarged,
			r.BaselineCost, r.ActualCost, r.Effect, r.EssNet,
			r.EssRemainingKwhStart, r.EssAvgCostStart, r.EssCostBasisStart,
			r.EssWithdrawnCost, r.EssRealizedProfit,
			r.EssAvgCostEnd, r.EssCostBasisEnd, r.EssResidualEnd,
		)
	}
	br := pool.SendBatch(ctx, batch)
	defer br.Close()
	for range rows {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("storage: upsert economics hourly: %w", err)
		}
	}
	return nil
}

// GetEconomicsHourly returns persisted per-hour rows for the half-open
// window [from, to), ordered by hour_start ascending.
func GetEconomicsHourly(ctx context.Context, pool *pgxpool.Pool, organizationID string, from, to time.Time) ([]EconomicsHourlyRow, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	rows, err := pool.Query(ctx, `
		SELECT
			organization_id, hour_start,
			rdn_uah_per_kwh, import_price_uah_per_kwh, export_price_uah_per_kwh,
			pv_generation_kwh, import_total_kwh, export_total_kwh, load_total_kwh,
			pv_to_load_kwh, pv_to_grid_kwh, grid_to_load_kwh,
			pv_to_ess_kwh, grid_to_ess_kwh, ess_to_load_kwh, ess_to_grid_kwh,
			ess_charged_kwh, ess_discharged_kwh,
			baseline_cost_uah, actual_cost_uah, effect_uah, ess_net_uah,
			ess_remaining_kwh_start, ess_avg_cost_uah_per_kwh_start, ess_cost_basis_uah_start,
			ess_withdrawn_cost_uah, ess_realized_profit_uah,
			ess_avg_cost_uah_per_kwh_end, ess_cost_basis_uah_end, ess_residual_kwh_end
		FROM economics_hourly
		WHERE organization_id = $1 AND hour_start >= $2 AND hour_start < $3
		ORDER BY hour_start ASC
	`, organizationID, from.UTC(), to.UTC())
	if err != nil {
		return nil, fmt.Errorf("storage: query economics hourly: %w", err)
	}
	defer rows.Close()
	var out []EconomicsHourlyRow
	for rows.Next() {
		var r EconomicsHourlyRow
		if err := rows.Scan(
			&r.OrganizationID, &r.HourStart,
			&r.Rdn, &r.ImportPrice, &r.ExportPrice,
			&r.PVGeneration, &r.ImportTotal, &r.ExportTotal, &r.LoadTotal,
			&r.PVToLoad, &r.PVToGrid, &r.GridToLoad,
			&r.PVToEss, &r.GridToEss, &r.EssToLoad, &r.EssToGrid,
			&r.EssCharged, &r.EssDischarged,
			&r.BaselineCost, &r.ActualCost, &r.Effect, &r.EssNet,
			&r.EssRemainingKwhStart, &r.EssAvgCostStart, &r.EssCostBasisStart,
			&r.EssWithdrawnCost, &r.EssRealizedProfit,
			&r.EssAvgCostEnd, &r.EssCostBasisEnd, &r.EssResidualEnd,
		); err != nil {
			return nil, fmt.Errorf("storage: scan economics hourly: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate economics hourly: %w", err)
	}
	return out, nil
}

// EconomicsDailyRow is one persisted per-day economics summary.
type EconomicsDailyRow struct {
	OrganizationID string
	Day            time.Time
	Tz             string

	BaselineCost float64
	ActualCost   float64
	Effect       float64
	EssNet       float64

	Load          float64
	PV            float64
	GridImport    float64
	GridExport    float64
	EssCharged    float64
	EssDischarged float64
	PVToLoad      float64
	PVToEss       float64
	PVToGrid      float64
	GridToLoad    float64
	GridToEss     float64
	EssToLoad     float64
	EssToGrid     float64

	AvgImportPrice float64
	AvgExportPrice float64

	RevenuePvExport  float64
	RevenuePvSelf    float64
	RevenueEssExport float64
	RevenueEssSelf   float64
	RevenueTotal     float64
	ExpenseGridCharge float64
	ExpenseTotal     float64
	Ebitda           float64

	EssWithdrawnCost      float64
	EssRealizedProfit     float64
	EssDegradationCost    float64
	EssAvgCostBasisEod    float64
	EssResidualKwhEod     float64
	EssCostBasisUahEod    float64

	HoursWithData     int
	HoursMissingPrice int
	SkipDiagnostics   string
	IsFinal           bool

	// ComputedAt is when this row was last written (now() on upsert).
	// Read-through callers use it to decide whether a still-open day
	// cached by the economics-recompute daemon is fresh enough to serve
	// without a live recompute.
	ComputedAt time.Time

	// Reconciled is true when the day's flows were scaled to canonical
	// FusionSolar daily KPIs. QualityFlags lists diagnostics (e.g.
	// "load_mismatch:0.07"); Reconciliation is the per-field
	// computed/canonical/factor JSON (nil when not reconciled).
	Reconciled    bool
	QualityFlags  []string
	Reconciliation json.RawMessage
}

// UpsertEconomicsDaily replaces the per-day summary by (org, day).
func UpsertEconomicsDaily(ctx context.Context, pool *pgxpool.Pool, r EconomicsDailyRow) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	const stmt = `
		INSERT INTO economics_daily (
			organization_id, day, tz,
			baseline_cost_uah, actual_cost_uah, effect_uah, ess_net_uah,
			load_kwh, pv_kwh, grid_import_kwh, grid_export_kwh, ess_charged_kwh, ess_discharged_kwh,
			pv_to_load_kwh, pv_to_ess_kwh, pv_to_grid_kwh, grid_to_load_kwh, grid_to_ess_kwh, ess_to_load_kwh, ess_to_grid_kwh,
			avg_import_price_uah_per_kwh, avg_export_price_uah_per_kwh,
			revenue_pv_export_uah, revenue_pv_self_uah, revenue_ess_export_uah, revenue_ess_self_uah, revenue_total_uah,
			expense_grid_charge_uah, expense_total_uah, ebitda_uah,
			ess_withdrawn_cost_uah, ess_realized_profit_uah, ess_degradation_cost_uah,
			ess_avg_cost_basis_uah_per_kwh_eod, ess_residual_kwh_eod, ess_cost_basis_uah_eod,
			hours_with_data, hours_missing_price, skip_diagnostics, is_final,
			reconciled, quality_flags, reconciliation, computed_at
		) VALUES (
			$1,$2::date,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,
			$21,$22,$23,$24,$25,$26,$27,$28,$29,$30,$31,$32,$33,$34,$35,$36,$37,$38,$39,$40,
			$41,$42,$43::jsonb, now()
		)
		ON CONFLICT (organization_id, day) DO UPDATE SET
			tz = EXCLUDED.tz,
			baseline_cost_uah = EXCLUDED.baseline_cost_uah,
			actual_cost_uah = EXCLUDED.actual_cost_uah,
			effect_uah = EXCLUDED.effect_uah,
			ess_net_uah = EXCLUDED.ess_net_uah,
			load_kwh = EXCLUDED.load_kwh,
			pv_kwh = EXCLUDED.pv_kwh,
			grid_import_kwh = EXCLUDED.grid_import_kwh,
			grid_export_kwh = EXCLUDED.grid_export_kwh,
			ess_charged_kwh = EXCLUDED.ess_charged_kwh,
			ess_discharged_kwh = EXCLUDED.ess_discharged_kwh,
			pv_to_load_kwh = EXCLUDED.pv_to_load_kwh,
			pv_to_ess_kwh = EXCLUDED.pv_to_ess_kwh,
			pv_to_grid_kwh = EXCLUDED.pv_to_grid_kwh,
			grid_to_load_kwh = EXCLUDED.grid_to_load_kwh,
			grid_to_ess_kwh = EXCLUDED.grid_to_ess_kwh,
			ess_to_load_kwh = EXCLUDED.ess_to_load_kwh,
			ess_to_grid_kwh = EXCLUDED.ess_to_grid_kwh,
			avg_import_price_uah_per_kwh = EXCLUDED.avg_import_price_uah_per_kwh,
			avg_export_price_uah_per_kwh = EXCLUDED.avg_export_price_uah_per_kwh,
			revenue_pv_export_uah = EXCLUDED.revenue_pv_export_uah,
			revenue_pv_self_uah = EXCLUDED.revenue_pv_self_uah,
			revenue_ess_export_uah = EXCLUDED.revenue_ess_export_uah,
			revenue_ess_self_uah = EXCLUDED.revenue_ess_self_uah,
			revenue_total_uah = EXCLUDED.revenue_total_uah,
			expense_grid_charge_uah = EXCLUDED.expense_grid_charge_uah,
			expense_total_uah = EXCLUDED.expense_total_uah,
			ebitda_uah = EXCLUDED.ebitda_uah,
			ess_withdrawn_cost_uah = EXCLUDED.ess_withdrawn_cost_uah,
			ess_realized_profit_uah = EXCLUDED.ess_realized_profit_uah,
			ess_degradation_cost_uah = EXCLUDED.ess_degradation_cost_uah,
			ess_avg_cost_basis_uah_per_kwh_eod = EXCLUDED.ess_avg_cost_basis_uah_per_kwh_eod,
			ess_residual_kwh_eod = EXCLUDED.ess_residual_kwh_eod,
			ess_cost_basis_uah_eod = EXCLUDED.ess_cost_basis_uah_eod,
			hours_with_data = EXCLUDED.hours_with_data,
			hours_missing_price = EXCLUDED.hours_missing_price,
			skip_diagnostics = EXCLUDED.skip_diagnostics,
			is_final = EXCLUDED.is_final,
			reconciled = EXCLUDED.reconciled,
			quality_flags = EXCLUDED.quality_flags,
			reconciliation = EXCLUDED.reconciliation,
			computed_at = now()
	`
	var reconciliation []byte
	if len(r.Reconciliation) > 0 {
		reconciliation = []byte(r.Reconciliation)
	}
	if _, err := pool.Exec(ctx, stmt,
		r.OrganizationID, r.Day, r.Tz,
		r.BaselineCost, r.ActualCost, r.Effect, r.EssNet,
		r.Load, r.PV, r.GridImport, r.GridExport, r.EssCharged, r.EssDischarged,
		r.PVToLoad, r.PVToEss, r.PVToGrid, r.GridToLoad, r.GridToEss, r.EssToLoad, r.EssToGrid,
		r.AvgImportPrice, r.AvgExportPrice,
		r.RevenuePvExport, r.RevenuePvSelf, r.RevenueEssExport, r.RevenueEssSelf, r.RevenueTotal,
		r.ExpenseGridCharge, r.ExpenseTotal, r.Ebitda,
		r.EssWithdrawnCost, r.EssRealizedProfit, r.EssDegradationCost,
		r.EssAvgCostBasisEod, r.EssResidualKwhEod, r.EssCostBasisUahEod,
		r.HoursWithData, r.HoursMissingPrice, r.SkipDiagnostics, r.IsFinal,
		r.Reconciled, r.QualityFlags, reconciliation,
	); err != nil {
		return fmt.Errorf("storage: upsert economics daily: %w", err)
	}
	return nil
}

// GetEconomicsDaily returns the per-day summary for (org, day). The bool
// is false when no row exists.
func GetEconomicsDaily(ctx context.Context, pool *pgxpool.Pool, organizationID string, day time.Time) (EconomicsDailyRow, bool, error) {
	var r EconomicsDailyRow
	if pool == nil {
		return r, false, fmt.Errorf("storage: nil pool")
	}
	err := pool.QueryRow(ctx, `
		SELECT
			organization_id, day, tz,
			baseline_cost_uah, actual_cost_uah, effect_uah, ess_net_uah,
			load_kwh, pv_kwh, grid_import_kwh, grid_export_kwh, ess_charged_kwh, ess_discharged_kwh,
			pv_to_load_kwh, pv_to_ess_kwh, pv_to_grid_kwh, grid_to_load_kwh, grid_to_ess_kwh, ess_to_load_kwh, ess_to_grid_kwh,
			avg_import_price_uah_per_kwh, avg_export_price_uah_per_kwh,
			revenue_pv_export_uah, revenue_pv_self_uah, revenue_ess_export_uah, revenue_ess_self_uah, revenue_total_uah,
			expense_grid_charge_uah, expense_total_uah, ebitda_uah,
			ess_withdrawn_cost_uah, ess_realized_profit_uah, ess_degradation_cost_uah,
			ess_avg_cost_basis_uah_per_kwh_eod, ess_residual_kwh_eod, ess_cost_basis_uah_eod,
			hours_with_data, hours_missing_price, skip_diagnostics, is_final,
			reconciled, quality_flags, reconciliation, computed_at
		FROM economics_daily
		WHERE organization_id = $1 AND day = $2::date
	`, organizationID, day).Scan(
		&r.OrganizationID, &r.Day, &r.Tz,
		&r.BaselineCost, &r.ActualCost, &r.Effect, &r.EssNet,
		&r.Load, &r.PV, &r.GridImport, &r.GridExport, &r.EssCharged, &r.EssDischarged,
		&r.PVToLoad, &r.PVToEss, &r.PVToGrid, &r.GridToLoad, &r.GridToEss, &r.EssToLoad, &r.EssToGrid,
		&r.AvgImportPrice, &r.AvgExportPrice,
		&r.RevenuePvExport, &r.RevenuePvSelf, &r.RevenueEssExport, &r.RevenueEssSelf, &r.RevenueTotal,
		&r.ExpenseGridCharge, &r.ExpenseTotal, &r.Ebitda,
		&r.EssWithdrawnCost, &r.EssRealizedProfit, &r.EssDegradationCost,
		&r.EssAvgCostBasisEod, &r.EssResidualKwhEod, &r.EssCostBasisUahEod,
		&r.HoursWithData, &r.HoursMissingPrice, &r.SkipDiagnostics, &r.IsFinal,
		&r.Reconciled, &r.QualityFlags, &r.Reconciliation, &r.ComputedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return r, false, nil
		}
		return r, false, fmt.Errorf("storage: query economics daily: %w", err)
	}
	return r, true, nil
}

// GetEconomicsDailyRange returns every persisted per-day summary for the
// inclusive civil-date span [from, to], ordered by day ascending. An
// empty slice (no error) means the org has no stored days in the range.
func GetEconomicsDailyRange(ctx context.Context, pool *pgxpool.Pool, organizationID string, from, to time.Time) ([]EconomicsDailyRow, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	if organizationID == "" {
		return nil, fmt.Errorf("storage: empty organization_id")
	}
	rows, err := pool.Query(ctx, `
		SELECT
			organization_id, day, tz,
			baseline_cost_uah, actual_cost_uah, effect_uah, ess_net_uah,
			load_kwh, pv_kwh, grid_import_kwh, grid_export_kwh, ess_charged_kwh, ess_discharged_kwh,
			pv_to_load_kwh, pv_to_ess_kwh, pv_to_grid_kwh, grid_to_load_kwh, grid_to_ess_kwh, ess_to_load_kwh, ess_to_grid_kwh,
			avg_import_price_uah_per_kwh, avg_export_price_uah_per_kwh,
			revenue_pv_export_uah, revenue_pv_self_uah, revenue_ess_export_uah, revenue_ess_self_uah, revenue_total_uah,
			expense_grid_charge_uah, expense_total_uah, ebitda_uah,
			ess_withdrawn_cost_uah, ess_realized_profit_uah, ess_degradation_cost_uah,
			ess_avg_cost_basis_uah_per_kwh_eod, ess_residual_kwh_eod, ess_cost_basis_uah_eod,
			hours_with_data, hours_missing_price, skip_diagnostics, is_final,
			reconciled, quality_flags, reconciliation, computed_at
		FROM economics_daily
		WHERE organization_id = $1 AND day >= $2::date AND day <= $3::date
		ORDER BY day ASC
	`, organizationID, from, to)
	if err != nil {
		return nil, fmt.Errorf("storage: query economics daily range: %w", err)
	}
	defer rows.Close()
	var out []EconomicsDailyRow
	for rows.Next() {
		var r EconomicsDailyRow
		if err := rows.Scan(
			&r.OrganizationID, &r.Day, &r.Tz,
			&r.BaselineCost, &r.ActualCost, &r.Effect, &r.EssNet,
			&r.Load, &r.PV, &r.GridImport, &r.GridExport, &r.EssCharged, &r.EssDischarged,
			&r.PVToLoad, &r.PVToEss, &r.PVToGrid, &r.GridToLoad, &r.GridToEss, &r.EssToLoad, &r.EssToGrid,
			&r.AvgImportPrice, &r.AvgExportPrice,
			&r.RevenuePvExport, &r.RevenuePvSelf, &r.RevenueEssExport, &r.RevenueEssSelf, &r.RevenueTotal,
			&r.ExpenseGridCharge, &r.ExpenseTotal, &r.Ebitda,
			&r.EssWithdrawnCost, &r.EssRealizedProfit, &r.EssDegradationCost,
			&r.EssAvgCostBasisEod, &r.EssResidualKwhEod, &r.EssCostBasisUahEod,
			&r.HoursWithData, &r.HoursMissingPrice, &r.SkipDiagnostics, &r.IsFinal,
			&r.Reconciled, &r.QualityFlags, &r.Reconciliation, &r.ComputedAt,
		); err != nil {
			return nil, fmt.Errorf("storage: scan economics daily range: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("storage: iterate economics daily range: %w", err)
	}
	return out, nil
}
