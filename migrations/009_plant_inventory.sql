-- Rare plant-inventory snapshots from SmartLogger (passport / nominals).
-- Populated by the collector's inventory poll (startup / hourly / daily),
-- not by the 1s telemetry loop. Mirrored by storage.InitPlantInventorySchema.
CREATE TABLE IF NOT EXISTS plant_inventory_snapshots (
    time                       timestamptz NOT NULL,
    organization_id            text NOT NULL,
    device_host                text NOT NULL DEFAULT '',
    poll_reason                text NOT NULL,
    pv_rated_kw                double precision,
    ess_rated_kw               double precision,
    ess_rated_kwh              double precision,
    ess_count                  double precision,
    pcs_count                  double precision,
    ess_soh_pct                double precision,
    active_power_control_mode  double precision,
    quality_flags              text[] NOT NULL DEFAULT '{}',
    raw                        jsonb NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS plant_inventory_snapshots_org_time_idx
    ON plant_inventory_snapshots (organization_id, time DESC);
