-- Generator in the economics (ems-spec generator handoff §4.5–§4.6).
-- Mirror of the generator part of storage.InitEconomicsSchema, which adds
-- these columns idempotently at API startup — apply manually only when
-- running migrations by hand. Rows written before them read as hours and
-- days without generator data.

-- Per hour: what the Encombi meter and supply mode showed (any hour with
-- generator data), and the flows and money when the hour was counted with
-- the generator (gen_accounted).
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS gen_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS gen_estimated boolean NOT NULL DEFAULT false;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS minutes_generator integer NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS minutes_island integer NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS minutes_blackout integer NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS minutes_transition integer NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS minutes_unknown integer NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS gen_accounted boolean NOT NULL DEFAULT false;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS gen_to_load_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS gen_to_ess_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS load_offgrid_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS pv_to_load_offgrid_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS ess_to_load_offgrid_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS pv_to_ess_offgrid_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS pv_to_load_island_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS ess_to_load_island_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS gen_fuel_cost_uah double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS gen_fuel_l double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS gen_fuel_method text NOT NULL DEFAULT '';
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS pv_offgrid_value_uah double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS ess_offgrid_value_uah double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_hourly ADD COLUMN IF NOT EXISTS gen_charge_cost_uah double precision NOT NULL DEFAULT 0;

-- Per day: the sums of the hours above. expense_gen_charge_uah is the fuel
-- spent charging the ESS, part of expense_total_uah.
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS gen_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS gen_to_load_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS gen_to_ess_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS load_offgrid_kwh double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS gen_fuel_cost_uah double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS gen_fuel_l double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS gen_fuel_method text NOT NULL DEFAULT '';
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS expense_gen_charge_uah double precision NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS minutes_generator integer NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS minutes_island integer NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS minutes_blackout integer NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS minutes_transition integer NOT NULL DEFAULT 0;
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS minutes_unknown integer NOT NULL DEFAULT 0;
