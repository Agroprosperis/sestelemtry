package pvplan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	// DefaultForecastURL serves the live forecast.
	DefaultForecastURL = "https://api.open-meteo.com/v1/forecast"
	// DefaultPreviousRunsURL serves archived forecasts at fixed lead
	// times; `_previous_day1` is the value predicted 24 h ahead.
	DefaultPreviousRunsURL = "https://previous-runs-api.open-meteo.com/v1/forecast"
)

// Plane is where and how a set of panels faces. Azimuth follows
// Open-Meteo: 0 = south, −90 = east, 90 = west, ±180 = north.
type Plane struct {
	Latitude   float64
	Longitude  float64
	TiltDeg    float64
	AzimuthDeg float64
}

// WeatherClient reads plane-of-array irradiance and air temperature
// from Open-Meteo.
type WeatherClient struct {
	forecastURL     string
	previousRunsURL string
	apiKey          string
	http            *http.Client
	retryDelays     []time.Duration
}

// NewWeatherClient returns a client for the given endpoints (the
// public ones when empty). apiKey is appended when set — commercial
// use of Open-Meteo needs one.
func NewWeatherClient(forecastURL, previousRunsURL, apiKey string, httpClient *http.Client) *WeatherClient {
	if forecastURL == "" {
		forecastURL = DefaultForecastURL
	}
	if previousRunsURL == "" {
		previousRunsURL = DefaultPreviousRunsURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 60 * time.Second}
	}
	return &WeatherClient{
		forecastURL:     forecastURL,
		previousRunsURL: previousRunsURL,
		apiKey:          apiKey,
		http:            httpClient,
		retryDelays:     []time.Duration{2 * time.Second, 10 * time.Second},
	}
}

// Forecast returns the live forecast for a plane from yesterday through
// the day after tomorrow (UTC days), which covers today and tomorrow in
// any zone near Kyiv.
func (c *WeatherClient) Forecast(ctx context.Context, p Plane) ([]HourInput, error) {
	q := planeQuery(p)
	q.Set("hourly", "temperature_2m,global_tilted_irradiance")
	q.Set("past_days", "1")
	q.Set("forecast_days", "3")
	return c.get(ctx, c.forecastURL, q, "temperature_2m", "global_tilted_irradiance")
}

// PreviousDay1 returns, for every hour of the UTC dates [fromDay,
// toDay], what was forecast for it 24 hours earlier.
func (c *WeatherClient) PreviousDay1(ctx context.Context, p Plane, fromDay, toDay string) ([]HourInput, error) {
	q := planeQuery(p)
	q.Set("hourly", "temperature_2m_previous_day1,global_tilted_irradiance_previous_day1")
	q.Set("start_date", fromDay)
	q.Set("end_date", toDay)
	return c.get(ctx, c.previousRunsURL, q, "temperature_2m_previous_day1", "global_tilted_irradiance_previous_day1")
}

func planeQuery(p Plane) url.Values {
	q := url.Values{}
	q.Set("latitude", strconv.FormatFloat(p.Latitude, 'f', -1, 64))
	q.Set("longitude", strconv.FormatFloat(p.Longitude, 'f', -1, 64))
	q.Set("tilt", strconv.FormatFloat(p.TiltDeg, 'f', -1, 64))
	q.Set("azimuth", strconv.FormatFloat(p.AzimuthDeg, 'f', -1, 64))
	q.Set("timeformat", "unixtime")
	return q
}

type openMeteoResponse struct {
	Hourly map[string]json.RawMessage `json:"hourly"`
	Reason string                     `json:"reason"`
}

// errPermanent marks a refusal retrying won't fix (a bad request). The
// planner's errors reach the passport, so they read in Ukrainian; the
// reason Open-Meteo gives stays as it sent it.
var errPermanent = errors.New("Open-Meteo відхилив запит")

func (c *WeatherClient) get(ctx context.Context, base string, q url.Values, tempKey, gtiKey string) ([]HourInput, error) {
	if c.apiKey != "" {
		q.Set("apikey", c.apiKey)
	}
	reqURL := base + "?" + q.Encode()
	var lastErr error
	for attempt := 0; ; attempt++ {
		hours, err := c.fetch(ctx, reqURL, tempKey, gtiKey)
		if err == nil {
			return hours, nil
		}
		lastErr = err
		if errors.Is(err, errPermanent) || ctx.Err() != nil || attempt >= len(c.retryDelays) {
			return nil, lastErr
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.retryDelays[attempt]):
		}
	}
}

func (c *WeatherClient) fetch(ctx context.Context, reqURL, tempKey, gtiKey string) ([]HourInput, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	var parsed openMeteoResponse
	_ = json.Unmarshal(body, &parsed)
	if res.StatusCode != http.StatusOK {
		msg := parsed.Reason
		if msg == "" {
			msg = res.Status
		}
		if res.StatusCode >= 400 && res.StatusCode < 500 && res.StatusCode != http.StatusTooManyRequests {
			return nil, fmt.Errorf("%w: %s", errPermanent, msg)
		}
		return nil, fmt.Errorf("Open-Meteo недоступний: %s", msg)
	}
	return parseHours(parsed.Hourly, tempKey, gtiKey)
}

// parseHours reads the hourly arrays. Open-Meteo stamps irradiance at
// the end of the hour it averages ("preceding hour mean"), so a value
// stamped 12:00 belongs to 11:00–12:00. The n8n flow read it as
// 12:00–13:00, which put its curve an hour late. Hours with a missing
// value are skipped.
func parseHours(hourly map[string]json.RawMessage, tempKey, gtiKey string) ([]HourInput, error) {
	var times []int64
	if err := json.Unmarshal(hourly["time"], &times); err != nil {
		return nil, fmt.Errorf("Open-Meteo: незрозуміла відповідь (hourly.time): %w", err)
	}
	var temps, gtis []*float64
	if err := json.Unmarshal(hourly[tempKey], &temps); err != nil {
		return nil, fmt.Errorf("Open-Meteo: незрозуміла відповідь (hourly.%s): %w", tempKey, err)
	}
	if err := json.Unmarshal(hourly[gtiKey], &gtis); err != nil {
		return nil, fmt.Errorf("Open-Meteo: незрозуміла відповідь (hourly.%s): %w", gtiKey, err)
	}
	if len(temps) != len(times) || len(gtis) != len(times) {
		return nil, fmt.Errorf("Open-Meteo: годинні ряди різної довжини")
	}
	out := make([]HourInput, 0, len(times))
	for i, ts := range times {
		if temps[i] == nil || gtis[i] == nil {
			continue
		}
		out = append(out, HourInput{
			Start:  time.Unix(ts, 0).UTC().Add(-time.Hour),
			TempC:  *temps[i],
			GtiWm2: *gtis[i],
		})
	}
	return out, nil
}
