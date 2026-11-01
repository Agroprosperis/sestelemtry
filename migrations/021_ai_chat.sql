-- 021: usage log of the AI assistant (POST /api/v1/chat).
-- Mirror of storage.InitAIChatSchema, which the API runs at startup —
-- apply manually only when running migrations by hand.
-- Apply locally with: supabase migration up

-- One row per assistant turn: who asked, about which organization,
-- which read-only tools ran and the token cost. The daily per-user
-- limit counts these rows. The conversation text is never stored.
-- user_id has no foreign key on purpose: the log outlives a deleted
-- account.
CREATE TABLE IF NOT EXISTS ai_chat_usage (
    id              bigserial PRIMARY KEY,
    user_id         bigint NOT NULL,
    organization_id text NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    model           text NOT NULL,
    tools           text[] NOT NULL DEFAULT '{}',
    input_tokens    integer NOT NULL DEFAULT 0,
    output_tokens   integer NOT NULL DEFAULT 0,
    status          text NOT NULL
);

CREATE INDEX IF NOT EXISTS ai_chat_usage_user_created
    ON ai_chat_usage (user_id, created_at);
