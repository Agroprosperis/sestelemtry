-- PV plan computed in-house from Open-Meteo, switched per station
-- against the n8n flow. Mirror of storage.InitPvPlanSchema, which
-- creates these tables idempotently at API startup — apply manually
-- only when running migrations by hand.

-- The station passport: which source plans it, and the station-level
-- inputs of the model. Coordinates left NULL fall back to config.yaml.
CREATE TABLE IF NOT EXISTS pv_plan_config (
    organization_id       text PRIMARY KEY,
    source                text NOT NULL DEFAULT 'n8n',
    latitude              double precision,
    longitude             double precision,
    pac_limit_kw          double precision NOT NULL DEFAULT 0,
    gamma_pct_per_c       double precision NOT NULL DEFAULT 0,
    whole_elevator_from   date,
    history_backfilled_at timestamptz,
    updated_at            timestamptz NOT NULL DEFAULT now(),
    updated_by            text NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS pv_orientations (
    organization_id text NOT NULL REFERENCES pv_plan_config (organization_id) ON DELETE CASCADE,
    idx             integer NOT NULL,
    tilt_deg        double precision NOT NULL,
    azimuth_deg     double precision NOT NULL,
    modules_count   integer NOT NULL,
    module_power_w  double precision NOT NULL,
    enabled         boolean NOT NULL DEFAULT true,
    note            text NOT NULL DEFAULT '',
    PRIMARY KEY (organization_id, idx)
);

CREATE TABLE IF NOT EXISTS pv_orientations_history (
    id              bigserial PRIMARY KEY,
    organization_id text NOT NULL,
    changed_at      timestamptz NOT NULL DEFAULT now(),
    changed_by      text NOT NULL DEFAULT '',
    changes         jsonb NOT NULL
);

CREATE INDEX IF NOT EXISTS pv_orientations_history_org_idx
    ON pv_orientations_history (organization_id, changed_at DESC);

-- The model's hourly plans. kind is day_ahead (frozen the evening
-- before; plan-vs-actual) or latest (freshest; control).
CREATE TABLE IF NOT EXISTS pv_plan_hourly (
    organization_id text NOT NULL,
    day             date NOT NULL,
    kind            text NOT NULL,
    hour            integer NOT NULL,
    planned_kwh     double precision NOT NULL,
    model_version   text NOT NULL,
    computed_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, day, kind, hour)
);

-- Where the plan's upstreams are (Open-Meteo addresses, the n8n
-- webhook), edited on the «Прогноз СЕС» page: one row. The Open-Meteo
-- key has its own column and is never returned by the API, only
-- whether one is stored.
CREATE TABLE IF NOT EXISTS pv_plan_settings (
    id             smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    settings       jsonb NOT NULL,
    open_meteo_key text NOT NULL DEFAULT '',
    updated_at     timestamptz NOT NULL DEFAULT now()
);
