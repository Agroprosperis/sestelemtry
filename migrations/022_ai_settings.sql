-- 022: settings of the AI assistant, edited on the dashboard's
-- «ШІ-асистент» page by administrators of every organization.
-- Mirror of storage.InitAIChatSchema, which the API runs at startup —
-- apply manually only when running migrations by hand.
-- Apply locally with: supabase migration up

-- A single row (id = 1). settings holds enabled / model / base_url /
-- reasoning_effort / daily_limit; the API key sits in its own column
-- and is never returned by the API, only whether one is stored.
CREATE TABLE IF NOT EXISTS ai_settings (
    id         smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    settings   jsonb NOT NULL,
    api_key    text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);
