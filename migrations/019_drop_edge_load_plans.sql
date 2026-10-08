-- 019: drop the old planner's operator load plan (013); desk loads
-- live in dispatch_versions (018).
-- Mirror of storage.InitEdgeSchema, which drops the table idempotently
-- at API startup — apply manually only when running migrations by hand.
-- Apply locally with: supabase migration up

DROP TABLE IF EXISTS edge_load_plans;
