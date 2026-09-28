-- 017: dashboard accounts, sessions and role grants.
-- Mirror of storage.InitAuthSchema, which creates these tables
-- idempotently at API startup — apply manually only when running
-- migrations by hand.
-- Apply locally with: supabase migration up

-- Local accounts. email is unique case-insensitively; password_hash is
-- empty for accounts that will sign in through an external provider
-- (auth_provider + external_subject), so OIDC can be added without a
-- schema change.
CREATE TABLE IF NOT EXISTS users (
    id               bigserial PRIMARY KEY,
    email            text NOT NULL,
    name             text NOT NULL DEFAULT '',
    password_hash    text NOT NULL DEFAULT '',
    disabled         boolean NOT NULL DEFAULT false,
    auth_provider    text NOT NULL DEFAULT 'local',
    external_subject text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS users_email_lower ON users (lower(email));
CREATE UNIQUE INDEX IF NOT EXISTS users_external_subject
    ON users (auth_provider, external_subject) WHERE external_subject <> '';

-- Browser sessions. Only the sha256 of the cookie secret is stored, so a
-- leaked table can't be replayed as cookies. expires_at slides forward
-- while the session is in use.
CREATE TABLE IF NOT EXISTS sessions (
    token_hash   bytea PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS sessions_user ON sessions (user_id);

-- Role assignments. organization_id NULL grants the role on every
-- organization, including ones added to config.yaml later. NULLS NOT
-- DISTINCT (PostgreSQL 15+) keeps a duplicate "all organizations" grant
-- out as well.
CREATE TABLE IF NOT EXISTS role_grants (
    user_id         bigint NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role            text NOT NULL,
    organization_id text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT role_grants_unique UNIQUE NULLS NOT DISTINCT (user_id, role, organization_id)
);
