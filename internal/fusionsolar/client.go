package fusionsolar

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultAPIBase is the FusionSolar data domain currently used (see the
// handoff doc). Override via FUSIONSOLAR_API_BASE if a deployment is
// homed on a different SmartPVMS region.
const DefaultAPIBase = "https://eu5.fusionsolar.huawei.com"

// deviceHistoryPath is the primary endpoint for the archive import:
// 5-minute historical device data, one device and <= 24h per request.
const deviceHistoryPath = "/rest/openapi/pvms/nbi/v1/device/history"

// stationDayPath is the canonical daily-KPI endpoint. One call returns
// every day of the month containing the supplied collectTime; used to
// reconcile the computed daily totals against FusionSolar UI numbers.
const stationDayPath = "/thirdData/getKpiStationDay"

// maxHistoryWindow is Huawei's documented per-request cap for the
// device/history endpoint. The importer chunks any wider range into
// successive sub-windows.
const maxHistoryWindow = 24 * time.Hour

// Client is a thin FusionSolar Northbound API client scoped to the
// historical device-data endpoint the importer needs. Credentials are
// supplied as a bearer access token (resolved from env by the caller);
// the client never reads or persists secrets itself.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient builds a client against `baseURL` (falling back to
// DefaultAPIBase when empty) authenticating with the given bearer
// access token.
func NewClient(baseURL, token string, timeout time.Duration) *Client {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultAPIBase
	}
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	return &Client{
		baseURL: base,
		token:   strings.TrimSpace(token),
		http:    &http.Client{Timeout: timeout},
	}
}

// APIBase returns the resolved FusionSolar data domain the client
// targets, for startup logging.
func (c *Client) APIBase() string { return c.baseURL }

// HistorySample is one 5-minute record for a single device: the
// collection timestamp plus the numeric fields Huawei returned (nulls
// dropped).
type HistorySample struct {
	Time   time.Time
	Fields map[string]float64
}

type historyRequest struct {
	DevDn     string `json:"devDn"`
	DevTypeID int    `json:"devTypeId"`
	StartTime int64  `json:"startTime"`
	EndTime   int64  `json:"endTime"`
}

type historyResponse struct {
	Success  *bool             `json:"success"`
	FailCode int               `json:"failCode"`
	Message  string            `json:"message"`
	Data     []json.RawMessage `json:"data"`
}

// DeviceHistory fetches the 5-minute history for one device over
// [start, end]. The window must not exceed maxHistoryWindow; the caller
// (Importer) is responsible for splitting larger ranges.
func (c *Client) DeviceHistory(
	ctx context.Context,
	devDn string,
	devTypeID int,
	start, end time.Time,
) ([]HistorySample, error) {
	if c.token == "" {
		return nil, fmt.Errorf("fusionsolar: missing access token")
	}
	if end.Sub(start) > maxHistoryWindow {
		return nil, fmt.Errorf("fusionsolar: window %s exceeds %s cap", end.Sub(start), maxHistoryWindow)
	}
	reqBody := historyRequest{
		DevDn:     devDn,
		DevTypeID: devTypeID,
		StartTime: start.UTC().UnixMilli(),
		EndTime:   end.UTC().UnixMilli(),
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("fusionsolar: marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+deviceHistoryPath, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("fusionsolar: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	// OpenAPI authenticates with the OAuth bearer token ONLY. Do not
	// also send XSRF-TOKEN: the gateway then treats the call as a
	// classic /thirdData session and rejects it with failCode=305
	// USER_MUST_RELOGIN (confirmed against the live eu5 endpoint).
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fusionsolar: device/history request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("fusionsolar: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fusionsolar: device/history HTTP %d: %s", resp.StatusCode, snippet(body))
	}

	var parsed historyResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("fusionsolar: decode response: %w (body: %s)", err, snippet(body))
	}
	// Huawei signals auth/quota/parameter problems via failCode even on
	// HTTP 200. Surface them verbatim so the operator sees e.g. "305"
	// (token expired) instead of a silently empty import.
	if parsed.Success != nil && !*parsed.Success || parsed.FailCode != 0 {
		return nil, fmt.Errorf("fusionsolar: device/history failCode=%d %s", parsed.FailCode, strings.TrimSpace(parsed.Message))
	}

	out := make([]HistorySample, 0, len(parsed.Data))
	for _, raw := range parsed.Data {
		sample, ok, err := parseHistoryRow(raw)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, sample)
		}
	}
	return out, nil
}

// StationDailyKPI is one day's canonical plant KPIs from
// getKpiStationDay. Day is the UTC midnight of the collectTime Huawei
// returned; the fields are the daily totals the dashboard reconciles
// against. Missing fields stay at zero (FusionSolar omits nulls).
type StationDailyKPI struct {
	Day           time.Time
	PVYield       float64 // PV generation, kWh
	UsePower      float64 // site consumption, kWh
	BuyPower      float64 // grid import, kWh
	OnGridPower   float64 // grid export, kWh
	ChargeCap     float64 // ESS charge total, kWh
	DischargeCap  float64 // ESS discharge total, kWh
	SelfUsePower  float64 // PV consumed (PVYield - export), kWh
}

type stationDayRequest struct {
	StationCodes string `json:"stationCodes"`
	CollectTime  int64  `json:"collectTime"`
}

// StationDaily fetches the canonical daily KPIs for the calendar month
// containing `monthAnchor` (Huawei returns the whole month for any
// collectTime inside it). `loc` is the plant's timezone, used to map
// each returned collectTime instant to its civil day (Huawei stamps the
// local-midnight boundary, which in a positive-offset zone falls on the
// previous UTC day). It targets the classic /thirdData Northbound
// endpoint authenticated with the OAuth bearer; if the gateway rejects
// that (e.g. failCode=305, requiring the classic XSRF session), the
// caller treats the error as non-fatal and skips reconciliation.
func (c *Client) StationDaily(ctx context.Context, stationCode string, monthAnchor time.Time, loc *time.Location) ([]StationDailyKPI, error) {
	if loc == nil {
		loc = time.UTC
	}
	if c.token == "" {
		return nil, fmt.Errorf("fusionsolar: missing access token")
	}
	reqBody := stationDayRequest{
		StationCodes: stationCode,
		CollectTime:  monthAnchor.UTC().UnixMilli(),
	}
	buf, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("fusionsolar: marshal station-day request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+stationDayPath, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("fusionsolar: build station-day request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fusionsolar: getKpiStationDay request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("fusionsolar: read station-day response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fusionsolar: getKpiStationDay HTTP %d: %s", resp.StatusCode, snippet(body))
	}

	var parsed historyResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("fusionsolar: decode station-day response: %w (body: %s)", err, snippet(body))
	}
	if parsed.Success != nil && !*parsed.Success || parsed.FailCode != 0 {
		return nil, fmt.Errorf("fusionsolar: getKpiStationDay failCode=%d %s", parsed.FailCode, strings.TrimSpace(parsed.Message))
	}

	out := make([]StationDailyKPI, 0, len(parsed.Data))
	for _, raw := range parsed.Data {
		sample, ok, err := parseHistoryRow(raw)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		f := sample.Fields
		// collectTime is the local-day boundary; resolve the civil day
		// in the plant tz and store it as a UTC-midnight key so the
		// economics lookup (civil Y/M/D) matches regardless of session tz.
		local := sample.Time.In(loc)
		out = append(out, StationDailyKPI{
			Day:          time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC),
			PVYield:      f["PVYield"],
			UsePower:     f["use_power"],
			BuyPower:     f["buyPower"],
			OnGridPower:  f["ongrid_power"],
			ChargeCap:    f["chargeCap"],
			DischargeCap: f["dischargeCap"],
			SelfUsePower: f["selfUsePower"],
		})
	}
	return out, nil
}

// parseHistoryRow extracts the collection timestamp and numeric fields
// from one history row. Huawei nests the values under `dataItemMap` on
// the classic Northbound shape; the newer OpenAPI flattens them onto
// the row. We support both: prefer dataItemMap, fall back to the
// top-level keys.
func parseHistoryRow(raw json.RawMessage) (HistorySample, bool, error) {
	var row map[string]json.RawMessage
	if err := json.Unmarshal(raw, &row); err != nil {
		return HistorySample{}, false, fmt.Errorf("fusionsolar: decode history row: %w", err)
	}
	ctRaw, ok := row["collectTime"]
	if !ok {
		return HistorySample{}, false, nil
	}
	var ctMs int64
	if err := json.Unmarshal(ctRaw, &ctMs); err != nil {
		// collectTime may arrive as a quoted number on some gateways.
		var ctStr string
		if err2 := json.Unmarshal(ctRaw, &ctStr); err2 != nil {
			return HistorySample{}, false, nil
		}
		parsed, perr := strconv.ParseInt(strings.TrimSpace(ctStr), 10, 64)
		if perr != nil {
			return HistorySample{}, false, nil
		}
		ctMs = parsed
	}
	if ctMs <= 0 {
		return HistorySample{}, false, nil
	}

	// The live OpenAPI nests values under `dataItems`; the classic
	// Northbound shape uses `dataItemMap`; some gateways flatten them
	// onto the row. Prefer a nested container, else fall back to the
	// top-level keys.
	fieldSource := row
	for _, key := range []string{"dataItems", "dataItemMap"} {
		if dim, ok := row[key]; ok {
			var nested map[string]json.RawMessage
			if err := json.Unmarshal(dim, &nested); err == nil && len(nested) > 0 {
				fieldSource = nested
				break
			}
		}
	}

	fields := make(map[string]float64, len(fieldSource))
	for k, v := range fieldSource {
		if k == "collectTime" || k == "devDn" || k == "dataItems" || k == "dataItemMap" {
			continue
		}
		if f, ok := rawToFloat(v); ok {
			fields[k] = f
		}
	}
	return HistorySample{
		Time:   time.UnixMilli(ctMs).UTC(),
		Fields: fields,
	}, true, nil
}

// rawToFloat decodes a JSON value that may be a number, a numeric
// string, or null into a float64. Returns ok=false for nulls,
// non-numeric strings, and unsupported types so the caller drops the
// field rather than writing a bogus zero.
func rawToFloat(raw json.RawMessage) (float64, bool) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		s := strings.TrimSpace(x)
		if s == "" {
			return 0, false
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func snippet(b []byte) string {
	const max = 300
	s := strings.TrimSpace(string(b))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}
