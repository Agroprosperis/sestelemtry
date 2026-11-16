-- The effect of a day's hours counted with the generator that spent
-- minutes without the grid, for the economics cards of the day, month,
-- year and portfolio. Mirror of storage.InitEconomicsSchema, which adds it
-- idempotently at API startup — apply manually only when running
-- migrations by hand.
ALTER TABLE economics_daily ADD COLUMN IF NOT EXISTS effect_offgrid_uah double precision NOT NULL DEFAULT 0;
