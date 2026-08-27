// Package pvplan reads the n8n generation-forecast flow — the same
// source the dashboard's day chart plots hour by hour — and rolls one
// site-day of it up into a single planned-kWh figure.
//
// Period plan-vs-fact needs one number per civil day, not the hourly
// shape, and a month or a year of those numbers is far more upstream
// calls than a page view can afford. So the flow is queried one day at
// a time and callers persist the result (see storage.PvPlanDaily): a
// past day's forecast never changes, which makes the cache permanent
// for everything but today.
package pvplan

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultWebhookURL is the n8n webhook serving the per-orientation
// hourly generation forecast (elevator_code + forecast_day → rows of
// {hour_ending, orientation_idx, planned_kwh}). Deployments override it
// through PV_FORECAST_WEBHOOK_URL.
const DefaultWebhookURL = "https://granary.app.n8n.cloud/webhook/96bac28d-5020-48b3-8f23-0bc189029c00"

// elevatorCodes maps organization ids to the n8n flow's elevator codes.
// Mirrors web/src/dashboard/transforms/pvForecast.ts and
// edgePvElevatorCode in internal/api/edge_planner.go. An org missing
// here (demo-org) has no forecast at all, which callers report as
// "unsupported" rather than as a zero plan.
var elevatorCodes = map[string]string{
	"ze":  "JE",
	"pe":  "RE",
	"pde": "PE",
	"ab":  "AB",
	"ke":  "KE",
	"de":  "DE",
	"sm":  "SM",
}

// ElevatorCodeFor resolves an organization id to its elevator code.
func ElevatorCodeFor(organizationID string) (string, bool) {
	code, ok := elevatorCodes[organizationID]
	return code, ok
}

// Client fetches single site-days from the forecast flow.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient returns a client for `baseURL` (DefaultWebhookURL when
// empty). A nil httpClient gets one with a per-day timeout, so a
// wedged upstream can't pin a request goroutine indefinitely.
func NewClient(baseURL string, httpClient *http.Client) *Client {
	if baseURL == "" {
		baseURL = DefaultWebhookURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{baseURL: baseURL, http: httpClient}
}

// DayHourly fetches one site-day and returns planned generation in kWh
// keyed by local hour start (0..23).
//
// An empty result (with no error) means the flow answered but holds no
// forecast for that day — it only keeps history back to when it was
// first deployed. That is a normal answer, not a failure: callers
// record the miss so they stop asking on every page view.
func (c *Client) DayHourly(ctx context.Context, elevatorCode string, day time.Time) (map[int]float64, error) {
	if elevatorCode == "" {
		return nil, fmt.Errorf("pvplan: empty elevator code")
	}
	reqURL := c.baseURL +
		"?elevator_code=" + url.QueryEscape(elevatorCode) +
		"&forecast_day=" + day.Format("2006-01-02")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pvplan: %s", res.Status)
	}
	// The flow returns a JSON array of rows, but answers a day it knows
	// nothing about with an object ({} or a message). Decode loosely so
	// that shape reads as "no forecast" instead of a decode failure.
	var raw json.RawMessage
	if err := json.NewDecoder(res.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("pvplan: decode: %w", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, nil
	}
	return AggregateHourly(rows), nil
}

// DayTotal returns the planned generation for one civil day in kWh.
// ok is false when the flow has no forecast for that day (see
// DayHourly): the day is reported as uncovered rather than folding a
// zero into a period total.
func (c *Client) DayTotal(ctx context.Context, elevatorCode string, day time.Time) (kwh float64, ok bool, err error) {
	byHour, err := c.DayHourly(ctx, elevatorCode, day)
	if err != nil {
		return 0, false, err
	}
	if len(byHour) == 0 {
		return 0, false, nil
	}
	for _, v := range byHour {
		kwh += v
	}
	return kwh, true, nil
}

// AggregateHourly sums planned_kwh across panel orientations for each
// hour, keyed by local hour start (the flow's hour_ending is 1..24, so
// hour_ending 12 lands on key 11). Repeated (hour_ending,
// orientation_idx) pairs are deduplicated last-wins and hours that
// don't add up to a positive figure are dropped — the exact logic of
// the dashboard's aggregatePvForecastHourly, so the day card, a period
// total, and the planner's PV curve can never disagree about the same
// day.
//
// Each row covers exactly one hour, so planned_kwh needs no interval
// scaling: the value is kWh for the hour and equally its average kW.
func AggregateHourly(rows []map[string]any) map[int]float64 {
	byHour := map[int]map[int]float64{}
	for _, r := range rows {
		hourEnding := int(anyToFloat(r["hour_ending"]))
		if hourEnding < 1 || hourEnding > 24 {
			continue
		}
		value := anyToFloat(r["planned_kwh"])
		if math.IsNaN(value) {
			continue
		}
		inner, ok := byHour[hourEnding]
		if !ok {
			inner = map[int]float64{}
			byHour[hourEnding] = inner
		}
		inner[int(anyToFloat(r["orientation_idx"]))] = value
	}
	out := make(map[int]float64, len(byHour))
	for hourEnding, inner := range byHour {
		sum := 0.0
		for _, v := range inner {
			sum += v
		}
		if sum > 0 {
			out[hourEnding-1] = sum
		}
	}
	return out
}

// anyToFloat coerces the loosely typed JSON values the flow emits
// (numbers, numeric strings, nulls) to float64. NaN signals "not a
// number I can use".
func anyToFloat(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return math.NaN()
		}
		return f
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil {
			return math.NaN()
		}
		return f
	case nil:
		return math.NaN()
	default:
		return math.NaN()
	}
}
