-- TimescaleDB telemetry schema (also applied programmatically by the collector).
CREATE EXTENSION IF NOT EXISTS timescaledb;

CREATE TABLE IF NOT EXISTS telemetry_samples (
	time timestamptz NOT NULL,
	organization_id text NOT NULL,
	metric_key text NOT NULL,
	value double precision NOT NULL,
	labels jsonb NOT NULL DEFAULT '{}'::jsonb
);

SELECT create_hypertable('telemetry_samples', 'time', if_not_exists => TRUE);

CREATE INDEX IF NOT EXISTS telemetry_samples_org_metric_time
	ON telemetry_samples (organization_id, metric_key, time DESC);
