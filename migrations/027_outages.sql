-- Planned grid outages from e-svitlo consumer cabinets (Vinnytsia /
-- Sumy). Mirror of storage.InitOutagesSchema, which creates these
-- tables idempotently at API startup — apply manually only when
-- running migrations by hand.

-- One cabinet account per operator host. The password has its own
-- column and is never returned by the API, only whether one is stored.
CREATE TABLE IF NOT EXISTS outage_accounts (
    host       text PRIMARY KEY,
    login      text NOT NULL DEFAULT '',
    password   text NOT NULL DEFAULT '',
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- One metering point (EIC) per station. host picks which cabinet to ask.
CREATE TABLE IF NOT EXISTS outage_points (
    organization_id text PRIMARY KEY,
    eic             text NOT NULL DEFAULT '',
    host            text NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- Planned off intervals for a station. Replaced wholesale on each
-- successful cabinet read for that organization.
CREATE TABLE IF NOT EXISTS outage_intervals (
    organization_id text NOT NULL,
    start_at        timestamptz NOT NULL,
    end_at          timestamptz NOT NULL,
    kind            text NOT NULL,
    PRIMARY KEY (organization_id, start_at, kind)
);

CREATE INDEX IF NOT EXISTS outage_intervals_org_range_idx
    ON outage_intervals (organization_id, start_at, end_at);

-- Last cabinet read per station (success or failure).
CREATE TABLE IF NOT EXISTS outage_reads (
    organization_id text PRIMARY KEY,
    attempted_at    timestamptz,
    succeeded_at    timestamptz,
    error           text NOT NULL DEFAULT ''
);
