package weather

import (
	"fmt"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

// BuildRows converts a parsed Open-Meteo forecast into storage rows for
// the given organization. Local-TZ ISO timestamps (`YYYY-MM-DDTHH:00`)
// are converted to UTC using the forecast's `utc_offset_seconds`, so the
// stored `hour` is canonical regardless of the location's timezone.
//
// `sourceURL` is recorded verbatim on every row so an operator can trace
// any stored sample back to the canonical Open-Meteo URL it came from.
//
// Returned slices may be shorter than the input arrays if Open-Meteo
// returns shorter parallel arrays for some fields (which it shouldn't,
// but we defend against it anyway — the index loop bounds the read).
func BuildRows(orgID string, f *Forecast, sourceURL string) ([]storage.WeatherHourlyRow, []storage.WeatherDailyRow, error) {
	if f == nil {
		return nil, nil, fmt.Errorf("weather: nil forecast")
	}

	hourly, err := buildHourly(orgID, f, sourceURL)
	if err != nil {
		return nil, nil, fmt.Errorf("hourly: %w", err)
	}
	daily, err := buildDaily(orgID, f, sourceURL)
	if err != nil {
		return nil, nil, fmt.Errorf("daily: %w", err)
	}
	return hourly, daily, nil
}

func buildHourly(orgID string, f *Forecast, sourceURL string) ([]storage.WeatherHourlyRow, error) {
	times := f.Hourly.Time
	out := make([]storage.WeatherHourlyRow, 0, len(times))
	for i, ts := range times {
		hour, err := parseLocalToUTC(ts, f.UTCOffsetSeconds)
		if err != nil {
			return nil, fmt.Errorf("hour[%d]=%q: %w", i, ts, err)
		}
		row := storage.WeatherHourlyRow{
			OrganizationID: orgID,
			Hour:           hour,
			SourceURL:      sourceURL,
			Temperature2mC: pickFloat(f.Hourly.Temperature2m, i),
			CloudCoverPct:  pickFloat(f.Hourly.CloudCover, i),
			IsDay:          pickBoolFromInt(f.Hourly.IsDay, i),
			ShortwaveWm2:   pickFloat(f.Hourly.ShortwaveRadiation, i),
			DirectWm2:      pickFloat(f.Hourly.DirectRadiation, i),
			DiffuseWm2:     pickFloat(f.Hourly.DiffuseRadiation, i),
			GtiInstantWm2:  pickFloat(f.Hourly.GlobalTiltedIrradiance, i),
		}
		out = append(out, row)
	}
	return out, nil
}

func buildDaily(orgID string, f *Forecast, sourceURL string) ([]storage.WeatherDailyRow, error) {
	times := f.Daily.Time
	out := make([]storage.WeatherDailyRow, 0, len(times))
	for i, ts := range times {
		day, err := time.Parse("2006-01-02", ts)
		if err != nil {
			return nil, fmt.Errorf("day[%d]=%q: %w", i, ts, err)
		}
		row := storage.WeatherDailyRow{
			OrganizationID:        orgID,
			Day:                   day,
			SourceURL:             sourceURL,
			Sunrise:               pickTime(f.Daily.Sunrise, i, f.UTCOffsetSeconds),
			Sunset:                pickTime(f.Daily.Sunset, i, f.UTCOffsetSeconds),
			DaylightDurationS:     pickFloat(f.Daily.DaylightDuration, i),
			SunshineDurationS:     pickFloat(f.Daily.SunshineDuration, i),
			ShortwaveRadiationSum: pickFloat(f.Daily.ShortwaveRadiationSum, i),
		}
		out = append(out, row)
	}
	return out, nil
}

// parseLocalToUTC converts an Open-Meteo local-TZ ISO string into UTC.
// Open-Meteo returns hourly times as `YYYY-MM-DDTHH:MM` (no offset
// suffix) in the location's local timezone. We parse them as UTC first
// and then subtract the offset to land at the canonical UTC instant.
// Some responses include seconds (`HH:MM:SS`), so we try both layouts.
func parseLocalToUTC(s string, offsetSeconds int) (time.Time, error) {
	t, err := time.Parse("2006-01-02T15:04", s)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05", s)
		if err != nil {
			return time.Time{}, err
		}
	}
	return t.Add(-time.Duration(offsetSeconds) * time.Second).UTC(), nil
}

func pickFloat(xs []*float64, i int) *float64 {
	if i < 0 || i >= len(xs) {
		return nil
	}
	return xs[i]
}

func pickBoolFromInt(xs []*int, i int) *bool {
	if i < 0 || i >= len(xs) {
		return nil
	}
	v := xs[i]
	if v == nil {
		return nil
	}
	b := *v != 0
	return &b
}

func pickTime(xs []*string, i int, offsetSeconds int) *time.Time {
	if i < 0 || i >= len(xs) {
		return nil
	}
	if xs[i] == nil {
		return nil
	}
	t, err := parseLocalToUTC(*xs[i], offsetSeconds)
	if err != nil {
		return nil
	}
	return &t
}
