package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WeatherHourlyRow is one hourly Open-Meteo forecast slot for a given
// organization, stored at hour-resolution in UTC. Numeric fields are
// pointers because Open-Meteo can return null for individual hours at
// the edges of the model window (e.g. solar radiation before sunrise on
// the first day is sometimes omitted).
//
// `FetchedAt` is populated by the read helper and ignored by the upsert
// helper (which always writes `now()`). It tells the API consumer how
// fresh the stored data is — for frozen past hours this is the moment
// they were last refreshed before the hour passed.
type WeatherHourlyRow struct {
	OrganizationID string
	Hour           time.Time
	Temperature2mC *float64
	CloudCoverPct  *float64
	IsDay          *bool
	ShortwaveWm2   *float64
	DirectWm2      *float64
	DiffuseWm2     *float64
	GtiInstantWm2  *float64
	SourceURL      string
	FetchedAt      time.Time
}

// WeatherDailyRow is one daily Open-Meteo summary for a given organization.
// `Sunrise` / `Sunset` are pointers because Open-Meteo can omit them at
// extreme latitudes during polar day/night. `FetchedAt` follows the
// same convention as on WeatherHourlyRow.
type WeatherDailyRow struct {
	OrganizationID        string
	Day                   time.Time
	Sunrise               *time.Time
	Sunset                *time.Time
	DaylightDurationS     *float64
	SunshineDurationS     *float64
	ShortwaveRadiationSum *float64
	SourceURL             string
	FetchedAt             time.Time
}

// InitWeatherSchema creates the weather forecast tables and supporting
// indexes (idempotent). Mirrors InitDAMSchema so the weather-collector
// can boot a fresh database without depending on the migration files.
func InitWeatherSchema(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS weather_forecast_hourly (
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
		)`,
		`CREATE INDEX IF NOT EXISTS weather_forecast_hourly_org_hour_idx
			ON weather_forecast_hourly (organization_id, hour DESC)`,
		`CREATE TABLE IF NOT EXISTS weather_forecast_daily (
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
		)`,
		`CREATE INDEX IF NOT EXISTS weather_forecast_daily_org_day_idx
			ON weather_forecast_daily (organization_id, day DESC)`,
	}
	for _, s := range stmts {
		if _, err := pool.Exec(ctx, s); err != nil {
			return fmt.Errorf("storage: exec weather schema: %w", err)
		}
	}
	return nil
}

// UpsertWeatherHourly inserts hourly forecast rows for (organization_id,
// hour). Past hours are frozen: the ON CONFLICT clause only refreshes
// rows whose `hour` is still in the future at upsert time, so once an
// hour has elapsed the values stored for it stay put for posterity.
// This lets the dashboard render "the forecast we had on day D" when
// viewing day D after the fact, rather than always seeing Open-Meteo's
// post-hoc reanalysis values.
//
// First-time inserts always succeed regardless of the hour, so a fresh
// deployment that runs catch-up against an older Open-Meteo window
// still captures historical values for hours where we had no data
// before.
func UpsertWeatherHourly(ctx context.Context, pool *pgxpool.Pool, rows []WeatherHourlyRow) error {
	if len(rows) == 0 {
		return nil
	}
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	const stmt = `
		INSERT INTO weather_forecast_hourly (
			organization_id, hour,
			temperature_2m_c, cloud_cover_pct, is_day,
			shortwave_wm2, direct_wm2, diffuse_wm2, gti_instant_wm2,
			source_url, fetched_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, now())
		ON CONFLICT (organization_id, hour) DO UPDATE SET
			temperature_2m_c = EXCLUDED.temperature_2m_c,
			cloud_cover_pct  = EXCLUDED.cloud_cover_pct,
			is_day           = EXCLUDED.is_day,
			shortwave_wm2    = EXCLUDED.shortwave_wm2,
			direct_wm2       = EXCLUDED.direct_wm2,
			diffuse_wm2      = EXCLUDED.diffuse_wm2,
			gti_instant_wm2  = EXCLUDED.gti_instant_wm2,
			source_url       = EXCLUDED.source_url,
			fetched_at       = now()
		WHERE weather_forecast_hourly.hour > now()
	`
	batch := &pgx.Batch{}
	for _, r := range rows {
		batch.Queue(stmt,
			r.OrganizationID, r.Hour.UTC(),
			r.Temperature2mC, r.CloudCoverPct, r.IsDay,
			r.ShortwaveWm2, r.DirectWm2, r.DiffuseWm2, r.GtiInstantWm2,
			r.SourceURL,
		)
	}
	br := pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < len(rows); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("storage: upsert weather hourly row %d: %w", i, err)
		}
	}
	return nil
}

// UpsertWeatherDaily inserts daily forecast rows for (organization_id,
// day). Same freeze-on-past semantics as UpsertWeatherHourly: rows for
// days strictly before CURRENT_DATE are left untouched on ON CONFLICT,
// so the dashboard can show the forecast as it stood on that day when
// browsing past periods. Today and future days keep refreshing on each
// collector tick.
func UpsertWeatherDaily(ctx context.Context, pool *pgxpool.Pool, rows []WeatherDailyRow) error {
	if len(rows) == 0 {
		return nil
	}
	if pool == nil {
		return fmt.Errorf("storage: nil pool")
	}
	const stmt = `
		INSERT INTO weather_forecast_daily (
			organization_id, day,
			sunrise, sunset,
			daylight_duration_s, sunshine_duration_s, shortwave_radiation_sum,
			source_url, fetched_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
		ON CONFLICT (organization_id, day) DO UPDATE SET
			sunrise                 = EXCLUDED.sunrise,
			sunset                  = EXCLUDED.sunset,
			daylight_duration_s     = EXCLUDED.daylight_duration_s,
			sunshine_duration_s     = EXCLUDED.sunshine_duration_s,
			shortwave_radiation_sum = EXCLUDED.shortwave_radiation_sum,
			source_url              = EXCLUDED.source_url,
			fetched_at              = now()
		WHERE weather_forecast_daily.day >= CURRENT_DATE
	`
	batch := &pgx.Batch{}
	for _, r := range rows {
		var sunrise, sunset any
		if r.Sunrise != nil {
			sunrise = r.Sunrise.UTC()
		}
		if r.Sunset != nil {
			sunset = r.Sunset.UTC()
		}
		batch.Queue(stmt,
			r.OrganizationID, r.Day,
			sunrise, sunset,
			r.DaylightDurationS, r.SunshineDurationS, r.ShortwaveRadiationSum,
			r.SourceURL,
		)
	}
	br := pool.SendBatch(ctx, batch)
	defer br.Close()
	for i := 0; i < len(rows); i++ {
		if _, err := br.Exec(); err != nil {
			return fmt.Errorf("storage: upsert weather daily row %d: %w", i, err)
		}
	}
	return nil
}

// QueryWeatherHourly returns hourly rows in [from, to] (inclusive on
// both ends) for the given organization, ordered ascending by hour.
func QueryWeatherHourly(
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID string,
	from, to time.Time,
) ([]WeatherHourlyRow, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	rows, err := pool.Query(ctx, `
		SELECT
			organization_id, hour,
			temperature_2m_c, cloud_cover_pct, is_day,
			shortwave_wm2, direct_wm2, diffuse_wm2, gti_instant_wm2,
			source_url, fetched_at
		FROM weather_forecast_hourly
		WHERE organization_id = $1
			AND hour >= $2
			AND hour <= $3
		ORDER BY hour ASC
	`, organizationID, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]WeatherHourlyRow, 0)
	for rows.Next() {
		var r WeatherHourlyRow
		if err := rows.Scan(
			&r.OrganizationID, &r.Hour,
			&r.Temperature2mC, &r.CloudCoverPct, &r.IsDay,
			&r.ShortwaveWm2, &r.DirectWm2, &r.DiffuseWm2, &r.GtiInstantWm2,
			&r.SourceURL, &r.FetchedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// QueryWeatherDaily returns daily rows in [from, to] (inclusive on both
// ends, as `date` values) for the given organization, ordered ascending.
func QueryWeatherDaily(
	ctx context.Context,
	pool *pgxpool.Pool,
	organizationID string,
	from, to time.Time,
) ([]WeatherDailyRow, error) {
	if pool == nil {
		return nil, fmt.Errorf("storage: nil pool")
	}
	rows, err := pool.Query(ctx, `
		SELECT
			organization_id, day,
			sunrise, sunset,
			daylight_duration_s, sunshine_duration_s, shortwave_radiation_sum,
			source_url, fetched_at
		FROM weather_forecast_daily
		WHERE organization_id = $1
			AND day >= $2::date
			AND day <= $3::date
		ORDER BY day ASC
	`, organizationID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]WeatherDailyRow, 0)
	for rows.Next() {
		var r WeatherDailyRow
		if err := rows.Scan(
			&r.OrganizationID, &r.Day,
			&r.Sunrise, &r.Sunset,
			&r.DaylightDurationS, &r.SunshineDurationS, &r.ShortwaveRadiationSum,
			&r.SourceURL, &r.FetchedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
