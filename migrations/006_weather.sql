-- Open-Meteo hourly + daily weather forecast cache per organization.
-- Populated by the weather-collector service and read by the API
-- (`/api/v1/weather-forecast`). PK on (organization_id, hour/day) so an
-- hourly refresh upserts the latest model output over future hours.
--
-- Forecast history is preserved at the row level: the Go upsert helper
-- (storage.UpsertWeatherHourly / UpsertWeatherDaily) refuses to update
-- rows whose `hour` is already in the past (or whose `day` is before
-- CURRENT_DATE). This freezes the forecast values that were stored
-- while the hour was still future, so the dashboard can render
-- "what we predicted for day D" when D is later viewed as a past day,
-- rather than seeing Open-Meteo's post-hoc reanalysis values.
--
-- Mirrored programmatically by storage.InitWeatherSchema, so a fresh
-- environment without the migration file (e.g. local dev) still works.
CREATE TABLE IF NOT EXISTS weather_forecast_hourly (
    organization_id     text NOT NULL,
    hour                timestamptz NOT NULL,
    temperature_2m_c    double precision,
    cloud_cover_pct     double precision,
    is_day              boolean,
    shortwave_wm2       double precision,
    direct_wm2          double precision,
    diffuse_wm2         double precision,
    gti_instant_wm2     double precision,
    source_url          text NOT NULL,
    fetched_at          timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, hour)
);

CREATE INDEX IF NOT EXISTS weather_forecast_hourly_org_hour_idx
    ON weather_forecast_hourly (organization_id, hour DESC);

CREATE TABLE IF NOT EXISTS weather_forecast_daily (
    organization_id          text NOT NULL,
    day                      date NOT NULL,
    sunrise                  timestamptz,
    sunset                   timestamptz,
    daylight_duration_s      double precision,
    sunshine_duration_s      double precision,
    shortwave_radiation_sum  double precision,
    source_url               text NOT NULL,
    fetched_at               timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (organization_id, day)
);

CREATE INDEX IF NOT EXISTS weather_forecast_daily_org_day_idx
    ON weather_forecast_daily (organization_id, day DESC);
