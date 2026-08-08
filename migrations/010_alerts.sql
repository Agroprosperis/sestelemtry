-- Per-device connectivity alert state for the alert-watchdog daemon.
-- One row per (organization, Modbus device host); the watchdog flips
-- `state` between 'ok' and 'down' and records when it last emailed about
-- the device, so a restart of the container does not re-send an alert
-- that operators already received. Mirrored by storage.InitAlertSchema.
CREATE TABLE IF NOT EXISTS device_alert_state (
    organization_id   text NOT NULL,
    device_host       text NOT NULL DEFAULT '',
    state             text NOT NULL,
    -- since: when the current state began (start of the outage for
    -- 'down'), used to render the outage duration in the email.
    since             timestamptz NOT NULL,
    -- last_sample_at: freshest telemetry timestamp seen for the device
    -- at the last check; NULL when nothing was found in the lookback
    -- window (device never reported, or has been down for a long time).
    last_sample_at    timestamptz,
    -- last_notified_at stays untouched when a send fails, so the next
    -- check retries instead of silently swallowing the alert.
    last_notified_at  timestamptz,
    PRIMARY KEY (organization_id, device_host)
);
