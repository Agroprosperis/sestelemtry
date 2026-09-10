-- 016: health snapshot on edge heartbeats.
-- Diagnostics spec (ems_edge_shadow_diagnostics.md §8.3): each ~30 s
-- heartbeat carries the full "очікувано vs факт" snapshot (checks,
-- BESS card, inverter fleet, SL alarm words). The cloud keeps only the
-- latest one per site, next to the heartbeat itself. Old edge builds
-- without the field leave the column NULL — the UI then hides the
-- diagnostics panels.
-- Apply locally with: supabase migration up

ALTER TABLE edge_heartbeats ADD COLUMN IF NOT EXISTS health jsonb;
