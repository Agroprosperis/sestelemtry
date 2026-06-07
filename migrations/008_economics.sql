-- 008_economics.sql
--
-- Server-side economics: date-versioned tariff schedule + persisted
-- per-hour and per-day economics results. The API process owns these
-- tables and bootstraps them programmatically at startup
-- (storage.InitEconomicsSchema), mirroring the organization_tariffs
-- pattern. This file is the manual-replay / documentation copy.

-- Date-versioned tariffs. The effective tariff for a given calendar
-- day is the row with the greatest effective_from <= day. Seeded from
-- the legacy single-blob organization_tariffs (effective_from epoch)
-- so existing orgs keep working.
CREATE TABLE IF NOT EXISTS organization_tariff_schedule (
	organization_id text NOT NULL,
	effective_from  date NOT NULL,
	tariffs         jsonb NOT NULL,
	updated_at      timestamptz NOT NULL DEFAULT now(),
	PRIMARY KEY (organization_id, effective_from)
);

INSERT INTO organization_tariff_schedule (organization_id, effective_from, tariffs)
SELECT organization_id, DATE '1970-01-01', tariffs
FROM organization_tariffs
ON CONFLICT (organization_id, effective_from) DO NOTHING;

-- Per-hour economics results. One row per (organization_id, hour_start).
-- Columns track the authoritative hourly EMS table; revenue legs / EBITDA
-- are daily aggregates and live in economics_daily.
CREATE TABLE IF NOT EXISTS economics_hourly (
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
);

CREATE INDEX IF NOT EXISTS economics_hourly_org_time_idx
	ON economics_hourly (organization_id, hour_start);

-- Per-day economics summary. One row per (organization_id, day).
-- is_final marks days strictly in the past (no more telemetry expected)
-- so reads can serve cache; non-final days are recomputed on read.
CREATE TABLE IF NOT EXISTS economics_daily (
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
);
