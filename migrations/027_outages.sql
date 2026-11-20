-- Planned grid outages from the e-svitlo consumer cabinets (Vinnytsia /
-- Sumy). Mirror of storage.InitOutagesSchema, which creates these tables
-- idempotently at API startup — apply manually only when running
-- migrations by hand.

-- A station's cabinet, set on its passport. The password has its own
-- column and is never returned by the API, only whether one is stored.
CREATE TABLE IF NOT EXISTS outage_station_accounts (
    organization_id text PRIMARY KEY,
    host            text NOT NULL,
    login           text NOT NULL,
    password        text NOT NULL DEFAULT '',
    eic             text NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- The station's planned outages. A read replaces those from the start of
-- its day on; earlier days stay, so the schedule can be held against what
-- the telemetry saw.
CREATE TABLE IF NOT EXISTS outage_intervals (
    organization_id text NOT NULL,
    start_at        timestamptz NOT NULL,
    end_at          timestamptz NOT NULL,
    kind            text NOT NULL,
    PRIMARY KEY (organization_id, start_at, kind)
);

CREATE INDEX IF NOT EXISTS outage_intervals_org_range_idx
    ON outage_intervals (organization_id, start_at, end_at);

-- The last read of each station, success or failure.
CREATE TABLE IF NOT EXISTS outage_reads (
    organization_id text PRIMARY KEY,
    attempted_at    timestamptz,
    succeeded_at    timestamptz,
    error           text NOT NULL DEFAULT ''
);
