// Package weather is the backend Open-Meteo client used by the
// weather-collector service. It mirrors the frontend's URL exactly
// (`web/src/api.ts`) so caching CDNs see the same canonical request,
// and it converts the local-TZ ISO strings Open-Meteo returns into
// canonical UTC `time.Time` values for storage.
package weather

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

// OpenMeteoBaseURL is the public Open-Meteo /v1/forecast endpoint.
const OpenMeteoBaseURL = "https://api.open-meteo.com/v1/forecast"

// Daily / hourly variables we request. Kept in sync with the
// `fetchOpenMeteoWeather` URL in `web/src/api.ts` so both the frontend
// and the collector hit the same cached URL.
const (
	dailyVars  = "sunrise,sunset,daylight_duration,sunshine_duration,shortwave_radiation_sum"
	hourlyVars = "temperature_2m,cloud_cover,is_day,shortwave_radiation,direct_radiation,diffuse_radiation,global_tilted_irradiance_instant"
)

// Forecast is the subset of the Open-Meteo /v1/forecast response that
// the collector consumes. Numeric ranges are *float64 so the JSON
// decoder preserves nulls (Open-Meteo can omit individual hours at the
// edge of the forecast window).
//
// `UTCOffsetSeconds` is the only header-level field we keep — it's the
// shift we apply to the local-TZ ISO timestamps to land them in UTC.
// Latitude/Longitude/Timezone from the response aren't used (we already
// know them, since we put them into the request).
type Forecast struct {
	UTCOffsetSeconds int `json:"utc_offset_seconds"`

	Hourly struct {
		Time                   []string   `json:"time"`
		Temperature2m          []*float64 `json:"temperature_2m"`
		CloudCover             []*float64 `json:"cloud_cover"`
		IsDay                  []*int     `json:"is_day"`
		ShortwaveRadiation     []*float64 `json:"shortwave_radiation"`
		DirectRadiation        []*float64 `json:"direct_radiation"`
		DiffuseRadiation       []*float64 `json:"diffuse_radiation"`
		GlobalTiltedIrradiance []*float64 `json:"global_tilted_irradiance_instant"`
	} `json:"hourly"`

	Daily struct {
		Time                  []string   `json:"time"`
		Sunrise               []*string  `json:"sunrise"`
		Sunset                []*string  `json:"sunset"`
		DaylightDuration      []*float64 `json:"daylight_duration"`
		SunshineDuration      []*float64 `json:"sunshine_duration"`
		ShortwaveRadiationSum []*float64 `json:"shortwave_radiation_sum"`
	} `json:"daily"`
}

// Client fetches Open-Meteo forecasts with bounded retry.
type Client struct {
	baseURL   *url.URL
	http      *http.Client
	userAgent string
}

// NewClient constructs a Client with sane defaults. Returns an error
// when `baseURL` (typically supplied by config) isn't a parseable URL —
// fail-fast at boot beats silently producing a malformed request URL
// on every fetch.
func NewClient(baseURL string, timeout time.Duration, userAgent string) (*Client, error) {
	if baseURL == "" {
		baseURL = OpenMeteoBaseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("weather: parse base_url %q: %w", baseURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("weather: base_url %q must be absolute", baseURL)
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if userAgent == "" {
		userAgent = "sestelemetry-weather/1.0"
	}
	return &Client{
		baseURL:   u,
		http:      &http.Client{Timeout: timeout},
		userAgent: userAgent,
	}, nil
}

// BuildURL returns the canonical Open-Meteo URL for the given coordinates.
// Identical shape to the frontend's `fetchOpenMeteoWeather` so the CDN sees
// the same canonical URL.
func (c *Client) BuildURL(latitude, longitude float64) string {
	u := *c.baseURL
	q := u.Query()
	q.Set("latitude", strconv.FormatFloat(latitude, 'f', -1, 64))
	q.Set("longitude", strconv.FormatFloat(longitude, 'f', -1, 64))
	q.Set("daily", dailyVars)
	q.Set("hourly", hourlyVars)
	q.Set("timezone", "auto")
	u.RawQuery = q.Encode()
	return u.String()
}

// Fetch downloads and parses the forecast for (latitude, longitude),
// retrying transient failures (5xx and network errors) up to `attempts`
// times with the supplied backoff between tries. Returns the parsed
// forecast and the URL that was hit (for storage as `source_url`).
func (c *Client) Fetch(
	ctx context.Context,
	latitude, longitude float64,
	attempts int,
	backoff time.Duration,
) (*Forecast, string, error) {
	url := c.BuildURL(latitude, longitude)
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 && backoff > 0 {
			select {
			case <-ctx.Done():
				return nil, url, ctx.Err()
			case <-time.After(backoff):
			}
		}
		f, err := c.tryFetch(ctx, url)
		if err == nil {
			return f, url, nil
		}
		lastErr = err
		if !isTransient(err) {
			return nil, url, fmt.Errorf("weather: fetch %s: %w", url, err)
		}
	}
	return nil, url, fmt.Errorf("weather: fetch %s failed after %d attempts: %w", url, attempts, lastErr)
}

func (c *Client) tryFetch(ctx context.Context, fetchURL string) (*Forecast, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpError{Status: resp.StatusCode}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	var f Forecast
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, &decodeError{Err: err}
	}
	return &f, nil
}

// httpError wraps a non-2xx status. Retried only when Status >= 500.
type httpError struct {
	Status int
}

func (e *httpError) Error() string {
	return fmt.Sprintf("status %d", e.Status)
}

// decodeError marks a JSON unmarshal failure. The response shape isn't
// going to change between retries, so we treat these as terminal and
// surface them up the stack immediately.
type decodeError struct {
	Err error
}

func (e *decodeError) Error() string {
	return fmt.Sprintf("decode body: %v", e.Err)
}

func (e *decodeError) Unwrap() error {
	return e.Err
}

func isTransient(err error) bool {
	var he *httpError
	if errors.As(err, &he) {
		return he.Status >= 500
	}
	var de *decodeError
	if errors.As(err, &de) {
		return false
	}
	// Network-level errors (timeouts, connection reset, DNS, EOF) are
	// treated as transient so a hiccup mid-fetch doesn't blank the
	// forecast.
	return true
}
