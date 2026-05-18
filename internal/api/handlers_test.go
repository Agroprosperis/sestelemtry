package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type mockStore struct {
	currentResp CurrentResponse
	currentErr  error
	seriesResp  TimeseriesResponse
	seriesErr   error
	summaryResp EnergySummaryResponse
	summaryErr  error
	damResp     DAMPricesResponse
	damErr      error
	weatherResp WeatherForecastResponse
	weatherErr  error
	readyErr    error

	currentAt time.Time

	damZone int
	damFrom time.Time
	damTo   time.Time

	weatherOrg  string
	weatherFrom time.Time
	weatherTo   time.Time

	summaryFrom time.Time
	summaryTo   time.Time
	summaryKeys []string

	samplesRows      []SampleRow
	samplesTruncated bool
	samplesErr       error

	samplesOrg   string
	samplesKeys  []string
	samplesFrom  time.Time
	samplesTo    time.Time
	samplesLimit int

	flowSources     []EnergyFlowRawRow
	flowSourcesErr  error
	flowSourcesOrg  string
	flowSourcesFrom time.Time
	flowSourcesTo   time.Time
}

func (m *mockStore) Current(_ context.Context, _ string, _ []string, at time.Time) (CurrentResponse, error) {
	m.currentAt = at
	return m.currentResp, m.currentErr
}

func (m *mockStore) Timeseries(_ context.Context, _ string, _ []string, _, _ time.Time, _, _ string, _ TimeseriesAggregation) (TimeseriesResponse, error) {
	return m.seriesResp, m.seriesErr
}

func (m *mockStore) EnergySummary(_ context.Context, _ string, metricKeys []string, from, to time.Time) (EnergySummaryResponse, error) {
	m.summaryFrom, m.summaryTo, m.summaryKeys = from, to, metricKeys
	return m.summaryResp, m.summaryErr
}

func (m *mockStore) DAMPrices(_ context.Context, zone int, from, to time.Time) (DAMPricesResponse, error) {
	m.damZone, m.damFrom, m.damTo = zone, from, to
	return m.damResp, m.damErr
}

func (m *mockStore) WeatherForecast(_ context.Context, orgID string, from, to time.Time) (WeatherForecastResponse, error) {
	m.weatherOrg, m.weatherFrom, m.weatherTo = orgID, from, to
	return m.weatherResp, m.weatherErr
}

func (m *mockStore) Samples(_ context.Context, orgID string, keys []string, from, to time.Time, limit int, emit func(SampleRow) error) (int, bool, error) {
	m.samplesOrg = orgID
	m.samplesKeys = append([]string(nil), keys...)
	m.samplesFrom, m.samplesTo, m.samplesLimit = from, to, limit
	if m.samplesErr != nil {
		return 0, false, m.samplesErr
	}
	emitted := 0
	for _, r := range m.samplesRows {
		if emitted >= limit {
			return emitted, true, nil
		}
		if err := emit(r); err != nil {
			return emitted, false, err
		}
		emitted++
	}
	return emitted, m.samplesTruncated, nil
}

func (m *mockStore) EnergyFlowSources(_ context.Context, orgID string, from, to time.Time, _ time.Duration) ([]EnergyFlowRawRow, error) {
	m.flowSourcesOrg = orgID
	m.flowSourcesFrom = from
	m.flowSourcesTo = to
	if m.flowSourcesErr != nil {
		return nil, m.flowSourcesErr
	}
	return m.flowSources, nil
}

func (m *mockStore) Ready(_ context.Context) error {
	return m.readyErr
}

func TestCurrentRequiresOrganizationID(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/current", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestCurrentSuccess(t *testing.T) {
	store := &mockStore{
		currentResp: CurrentResponse{
			OrganizationID: "org-a",
			Metrics: map[string]CurrentMetric{
				"soc_percent": {MetricKey: "soc_percent", Value: 86},
			},
		},
	}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/current?organization_id=org-a", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var got CurrentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OrganizationID != "org-a" {
		t.Fatalf("unexpected org id: %s", got.OrganizationID)
	}
}

func TestCurrentPropagatesAtParam(t *testing.T) {
	store := &mockStore{currentResp: CurrentResponse{OrganizationID: "org-a"}}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/current?organization_id=org-a&at=2026-05-02T12:34:56Z", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	want := time.Date(2026, 5, 2, 12, 34, 56, 0, time.UTC)
	if !store.currentAt.Equal(want) {
		t.Fatalf("at mismatch: want %v got %v", want, store.currentAt)
	}
}

func TestCurrentRejectsBadAt(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/current?organization_id=org-a&at=not-a-date", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestTimeseriesRequiresMetricKey(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/timeseries?organization_id=org-a", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestDashboardConfigEndpoint(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard-config", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
}

func TestOrganizationsListEmpty(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	var got OrganizationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Organizations == nil {
		t.Fatalf("expected non-nil slice (even if empty), got nil")
	}
	if len(got.Organizations) != 0 {
		t.Fatalf("expected empty slice, got %+v", got.Organizations)
	}
}

func TestOrganizationsListReturnsSetEntries(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetOrganizations([]OrganizationInfo{
		{
			ID:   "ze",
			Name: "ZE",
			Location: &LocationInfo{
				Latitude:  49.0191004,
				Longitude: 28.1260144,
				City:      "Жмеринка",
			},
		},
		{ID: "demo-org", Name: "Demo organization"},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organizations", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var got OrganizationsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Organizations) != 2 {
		t.Fatalf("want 2 orgs, got %d", len(got.Organizations))
	}
	if got.Organizations[0].ID != "ze" || got.Organizations[0].Location == nil {
		t.Fatalf("ze entry missing location: %+v", got.Organizations[0])
	}
	if got.Organizations[0].Location.City != "Жмеринка" {
		t.Fatalf("city mismatch: %q", got.Organizations[0].Location.City)
	}
	if got.Organizations[1].Location != nil {
		t.Fatalf("demo-org should have no location, got %+v", got.Organizations[1].Location)
	}
}

func TestOrganizationsListRejectsNonGet(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/organizations", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 got %d", rec.Code)
	}
}

func TestReadyzSuccess(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
}

func TestReadyzFailure(t *testing.T) {
	h := NewHandlers(&mockStore{readyErr: errors.New("db down")}, "*")
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 got %d", rec.Code)
	}
}

func TestSecurityHeadersPresent(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("expected nosniff, got %q", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("expected DENY, got %q", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("expected no-referrer, got %q", got)
	}
}

func TestSwaggerUIEndpoint(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/swagger", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("expected html content-type, got %q", got)
	}
}

func TestSwaggerSpecEndpoint(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/swagger/openapi.yaml", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/yaml") {
		t.Fatalf("expected yaml content-type, got %q", got)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "openapi: 3.0.3") {
		t.Fatalf("expected openapi version in body, got %q", body)
	}
}

func TestDAMPricesDefaultZoneAndDate(t *testing.T) {
	priceA := 5600.0
	store := &mockStore{
		damResp: DAMPricesResponse{
			Zone:   2,
			Prices: []DAMPrice{{DeliveryDate: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC), Hour: 1, Zone: 2, PriceUAHPerMWh: &priceA}},
		},
	}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dam-prices", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.damZone != 2 {
		t.Fatalf("expected default zone=2, got %d", store.damZone)
	}
	var got DAMPricesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Prices) != 1 || got.Prices[0].Hour != 1 || got.Prices[0].PriceUAHPerMWh == nil || *got.Prices[0].PriceUAHPerMWh != 5600.0 {
		t.Fatalf("unexpected payload: %#v", got)
	}
}

func TestDAMPricesParsesQueryParams(t *testing.T) {
	store := &mockStore{}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/dam-prices?zone=1&from=2026-04-29&to=2026-05-01", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.damZone != 1 {
		t.Fatalf("zone=1 not propagated, got %d", store.damZone)
	}
	wantFrom := time.Date(2026, 4, 29, 0, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if !store.damFrom.Equal(wantFrom) || !store.damTo.Equal(wantTo) {
		t.Fatalf("date range mismatch: from=%v to=%v", store.damFrom, store.damTo)
	}
}

func TestDAMPricesRejectsBadParams(t *testing.T) {
	cases := []string{
		"/api/v1/dam-prices?zone=abc",
		"/api/v1/dam-prices?zone=0",
		"/api/v1/dam-prices?zone=2&from=not-a-date",
		"/api/v1/dam-prices?zone=2&from=2026-05-02&to=2026-05-01",
	}
	for _, url := range cases {
		t.Run(url, func(t *testing.T) {
			h := NewHandlers(&mockStore{}, "*")
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rec := httptest.NewRecorder()
			h.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400, got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDAMPricesHidesInternalError(t *testing.T) {
	h := NewHandlers(&mockStore{damErr: errors.New("db down")}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dam-prices", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Fatalf("expected generic error body, got %q", rec.Body.String())
	}
}

func TestWeatherForecastRequiresOrgID(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/weather-forecast", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestWeatherForecastDefaultRangeIsTodayPlus2(t *testing.T) {
	temp := 12.3
	cloud := 40.0
	store := &mockStore{
		weatherResp: WeatherForecastResponse{
			OrganizationID: "org-a",
			Hourly: []WeatherForecastHour{
				{Hour: time.Now().UTC().Truncate(time.Hour), Temperature2mC: &temp, CloudCoverPct: &cloud},
			},
		},
	}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/weather-forecast?organization_id=org-a", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.weatherOrg != "org-a" {
		t.Fatalf("orgID not forwarded: %q", store.weatherOrg)
	}
	// from..to span is 2 days + (24h-1ns) of `to` expansion = 3 days - 1ns.
	want := 3*24*time.Hour - time.Nanosecond
	if got := store.weatherTo.Sub(store.weatherFrom); got != want {
		t.Fatalf("default range span: got %v want %v", got, want)
	}
	var resp WeatherForecastResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Hourly) != 1 {
		t.Fatalf("expected 1 hourly point, got %d", len(resp.Hourly))
	}
	if resp.Hourly[0].Temperature2mC == nil || *resp.Hourly[0].Temperature2mC != 12.3 {
		t.Fatalf("temp not echoed: %+v", resp.Hourly[0])
	}
}

func TestWeatherForecastParsesDateRange(t *testing.T) {
	store := &mockStore{}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/weather-forecast?organization_id=org-a&from=2026-05-15&to=2026-05-17", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	wantFrom := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	if !store.weatherFrom.Equal(wantFrom) {
		t.Fatalf("from: got %v want %v", store.weatherFrom, wantFrom)
	}
	wantTo := time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC).Add(24*time.Hour - time.Nanosecond)
	if !store.weatherTo.Equal(wantTo) {
		t.Fatalf("to expansion: got %v want %v", store.weatherTo, wantTo)
	}
}

func TestWeatherForecastRejectsBadParams(t *testing.T) {
	cases := []string{
		"/api/v1/weather-forecast?organization_id=org-a&from=not-a-date",
		"/api/v1/weather-forecast?organization_id=org-a&from=2026-05-02&to=2026-05-01",
		"/api/v1/weather-forecast?organization_id=org-a&from=2026-01-01&to=2026-12-31",
	}
	h := NewHandlers(&mockStore{}, "*")
	for _, u := range cases {
		req := httptest.NewRequest(http.MethodGet, u, nil)
		rec := httptest.NewRecorder()
		h.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: want 400 got %d", u, rec.Code)
		}
	}
}

func TestWeatherForecastHidesInternalError(t *testing.T) {
	h := NewHandlers(&mockStore{weatherErr: errors.New("db down")}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/weather-forecast?organization_id=org-a", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 got %d", rec.Code)
	}
}

func TestCurrentHidesInternalError(t *testing.T) {
	store := &mockStore{currentErr: errors.New("sql: no rows in result set")}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/current?organization_id=org-a", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("expected generic error body, got %q", body)
	}
	if strings.Contains(body, "sql: no rows") {
		t.Fatalf("expected internal error to be hidden, got %q", body)
	}
}

// TestSamplesStreamsCsv verifies the happy path: header row, BOM, one
// CSV row per emitted SampleRow, labels rendered as JSON strings.
func TestSamplesStreamsCsv(t *testing.T) {
	store := &mockStore{
		samplesRows: []SampleRow{
			{
				Time:      time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC),
				MetricKey: "active_pv_power_kw",
				Value:     12.345,
			},
			{
				Time:      time.Date(2026, 5, 9, 10, 0, 1, 0, time.UTC),
				MetricKey: "soc_percent",
				Value:     86.5,
				Labels:    map[string]string{"unit_id": "ess-1"},
			},
		},
	}
	h := NewHandlers(store, "*")
	url := "/api/v1/samples?organization_id=org-a&metric_keys=active_pv_power_kw,soc_percent" +
		"&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/csv") {
		t.Fatalf("expected text/csv, got %q", got)
	}
	cd := rec.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "samples_org-a_") || !strings.HasSuffix(cd, ".csv\"") {
		t.Fatalf("unexpected Content-Disposition: %q", cd)
	}
	body := rec.Body.String()
	if !strings.HasPrefix(body, "\xef\xbb\xbf") {
		t.Fatalf("expected UTF-8 BOM prefix")
	}
	body = strings.TrimPrefix(body, "\xef\xbb\xbf")
	want := "time,metric_key,modbus_register,data_type,gain,value,labels\r\n" +
		"2026-05-09T10:00:00Z,active_pv_power_kw,40388,UINT32,0.001,12.345,\r\n" +
		"2026-05-09T10:00:01Z,soc_percent,40515,UINT16,0.1,86.5,\"{\"\"unit_id\"\":\"\"ess-1\"\"}\"\r\n"
	if body != want {
		t.Fatalf("body mismatch\n got %q\nwant %q", body, want)
	}
	if store.samplesOrg != "org-a" {
		t.Fatalf("org mismatch: %q", store.samplesOrg)
	}
	if len(store.samplesKeys) != 2 || store.samplesKeys[0] != "active_pv_power_kw" {
		t.Fatalf("keys mismatch: %#v", store.samplesKeys)
	}
	if store.samplesLimit != defaultSamplesLimit {
		t.Fatalf("default limit mismatch: %d", store.samplesLimit)
	}
}

// TestSamplesAppendsTruncationSentinel verifies that when the store
// reports truncated=true (more rows than limit) the handler writes a
// final `__TRUNCATED__,...` row so the frontend can warn the user.
// The sentinel must keep the 7-column shape (time, metric_key,
// modbus_register, data_type, gain, value, labels) so RFC4180
// parsers don't choke on a ragged last line.
func TestSamplesAppendsTruncationSentinel(t *testing.T) {
	store := &mockStore{
		samplesRows: []SampleRow{
			{Time: time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC), MetricKey: "soc_percent", Value: 80},
			{Time: time.Date(2026, 5, 9, 10, 0, 1, 0, time.UTC), MetricKey: "soc_percent", Value: 81},
			{Time: time.Date(2026, 5, 9, 10, 0, 2, 0, time.UTC), MetricKey: "soc_percent", Value: 82},
		},
	}
	h := NewHandlers(store, "*")
	url := "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent" +
		"&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z&limit=2"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	body := strings.TrimPrefix(rec.Body.String(), "\xef\xbb\xbf")
	lines := strings.Split(strings.TrimRight(body, "\r\n"), "\r\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines (header + 2 rows + sentinel), got %d: %#v", len(lines), lines)
	}
	if !strings.HasPrefix(lines[3], "__TRUNCATED__,") {
		t.Fatalf("expected truncation sentinel, got %q", lines[3])
	}
	// Parse the sentinel through encoding/csv so quoted JSON commas
	// don't get counted as field separators; the row must have the
	// same 7-field shape as a regular data row.
	rec3, err := csv.NewReader(strings.NewReader(lines[3])).Read()
	if err != nil {
		t.Fatalf("parse sentinel: %v", err)
	}
	if len(rec3) != 7 {
		t.Fatalf("sentinel must have 7 fields, got %d: %#v", len(rec3), rec3)
	}
}

// TestSamplesEmitsBlankMetadataForUnknownMetricKey verifies the
// fall-through behavior when a metric_key isn't in
// ModbusRegisterMetadata: the row still streams correctly with empty
// modbus_register/data_type/gain cells instead of breaking the CSV
// shape.
func TestSamplesEmitsBlankMetadataForUnknownMetricKey(t *testing.T) {
	store := &mockStore{
		samplesRows: []SampleRow{
			{
				Time:      time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC),
				MetricKey: "synthetic_only",
				Value:     1.5,
			},
		},
	}
	h := NewHandlers(store, "*")
	url := "/api/v1/samples?organization_id=org-a&metric_keys=synthetic_only" +
		"&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	body := strings.TrimPrefix(rec.Body.String(), "\xef\xbb\xbf")
	want := "time,metric_key,modbus_register,data_type,gain,value,labels\r\n" +
		"2026-05-09T10:00:00Z,synthetic_only,,,,1.5,\r\n"
	if body != want {
		t.Fatalf("body mismatch\n got %q\nwant %q", body, want)
	}
}

// TestRegistersEndpoint locks in the JSON contract for
// /api/v1/registers — the dashboard fetches this once at startup to
// build the metric_key → register map used in CSV header annotation.
func TestRegistersEndpoint(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/registers", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	var got RegistersResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	pv, ok := got.Metadata["active_pv_power_kw"]
	if !ok {
		t.Fatalf("expected active_pv_power_kw in response, got %#v", got.Metadata)
	}
	if pv.Address != 40388 || pv.DataType != "UINT32" || pv.Gain != 0.001 {
		t.Fatalf("unexpected PV meta: %#v", pv)
	}
}

// TestSamplesRoundsValueToGainPrecision verifies that float64
// jitter from the decoder's `int * gain` (e.g. 156 * 0.1 yielding
// 15.600000000000001) is stripped from the CSV using the metric's
// declared decimal precision. Without this analysts get a column
// of weird trailing-9's that obscures the real readings.
func TestSamplesRoundsValueToGainPrecision(t *testing.T) {
	store := &mockStore{
		samplesRows: []SampleRow{
			// gain=0.1 (UINT16) — soc_percent. Real raw int 156
			// → 156 * 0.1 = 15.600000000000001 in IEEE-754.
			{
				Time:      time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC),
				MetricKey: "soc_percent",
				Value:     15.600000000000001,
			},
			// gain=0.01 (INT64) — total_energy_discharged_kwh.
			// 12009854 * 0.01 = 120098.54000000001 in IEEE-754.
			{
				Time:      time.Date(2026, 5, 9, 10, 0, 1, 0, time.UTC),
				MetricKey: "total_energy_discharged_kwh",
				Value:     120098.54000000001,
			},
			// Unknown metric — no gain available, must fall back
			// to shortest round-trip so we don't silently truncate
			// genuine precision (e.g. an averaged value from a
			// hypothetical synthetic exporter).
			{
				Time:      time.Date(2026, 5, 9, 10, 0, 2, 0, time.UTC),
				MetricKey: "synthetic_only",
				Value:     1.234567,
			},
		},
	}
	h := NewHandlers(store, "*")
	url := "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent" +
		"&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d", rec.Code)
	}
	body := strings.TrimPrefix(rec.Body.String(), "\xef\xbb\xbf")
	if !strings.Contains(body, ",15.6,") {
		t.Fatalf("expected SOC trimmed to 15.6, got body=%q", body)
	}
	if !strings.Contains(body, ",120098.54,") {
		t.Fatalf("expected discharged_kwh trimmed to 120098.54, got body=%q", body)
	}
	if !strings.Contains(body, ",1.234567,") {
		t.Fatalf("expected synthetic value preserved as 1.234567, got body=%q", body)
	}
}

// TestSamplesRendersTimeInRequestedTZ verifies that the `tz` query
// parameter shifts the rendered `time` column out of UTC. We pick
// Europe/Kyiv (UTC+3 in May) and assert the offset suffix is present
// — the underlying timestamp is identical, only the formatting
// changes. Without this the dashboard's day picker says "9 May" but
// the CSV starts at "8 May 21:00" which is what the analyst flagged.
func TestSamplesRendersTimeInRequestedTZ(t *testing.T) {
	store := &mockStore{
		samplesRows: []SampleRow{
			{
				Time:      time.Date(2026, 5, 9, 10, 0, 0, 0, time.UTC),
				MetricKey: "soc_percent",
				Value:     86.5,
			},
		},
	}
	h := NewHandlers(store, "*")
	url := "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent" +
		"&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z&tz=Europe/Kyiv"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	body := strings.TrimPrefix(rec.Body.String(), "\xef\xbb\xbf")
	if !strings.Contains(body, "2026-05-09T13:00:00+03:00,soc_percent") {
		t.Fatalf("expected Kyiv-local timestamp with +03:00 offset, got body=%q", body)
	}
}

// TestSamplesRejectsUnknownTZ guards the "typo means silent UTC
// fallback" failure mode — analysts would otherwise chase phantom
// drift if a misspelled zone name (Europe/Kyev) downgraded to UTC.
func TestSamplesRejectsUnknownTZ(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	url := "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent" +
		"&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z&tz=Europe/Atlantis"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "tz must be a valid IANA timezone") {
		t.Fatalf("expected explanatory error, got %q", rec.Body.String())
	}
}

// TestSamplesValidatesInputs locks in the bad-request paths so a
// future regression can't accidentally accept a 6-month range or a
// limit of 50_000_000 rows.
func TestSamplesValidatesInputs(t *testing.T) {
	cases := []struct {
		name string
		url  string
	}{
		{
			name: "missing organization_id",
			url:  "/api/v1/samples?metric_keys=soc_percent&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z",
		},
		{
			name: "missing metric_keys",
			url:  "/api/v1/samples?organization_id=org-a&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z",
		},
		{
			name: "missing from",
			url:  "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent&to=2026-05-10T00:00:00Z",
		},
		{
			name: "to before from",
			url:  "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent&from=2026-05-10T00:00:00Z&to=2026-05-09T00:00:00Z",
		},
		{
			name: "range over 31 days",
			url:  "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent&from=2026-01-01T00:00:00Z&to=2026-03-01T00:00:00Z",
		},
		{
			name: "limit not an integer",
			url:  "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z&limit=many",
		},
		{
			name: "limit above hard cap",
			url:  "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z&limit=6000000",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandlers(&mockStore{}, "*")
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			rec := httptest.NewRecorder()
			h.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// TestSamplesRejectsTooManyMetrics ensures the per-request metric
// fan-out stays bounded so a single export can't hammer the index
// for hundreds of keys at once.
func TestSamplesRejectsTooManyMetrics(t *testing.T) {
	keys := make([]string, maxSamplesMetricKeys+1)
	for i := range keys {
		keys[i] = fmt.Sprintf("metric_%d", i)
	}
	url := "/api/v1/samples?organization_id=org-a&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z&metric_keys=" +
		strings.Join(keys, ",")
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestTimeseriesHidesInternalError(t *testing.T) {
	store := &mockStore{seriesErr: errors.New("db timeout")}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/timeseries?organization_id=org-a&metric_key=soc_percent", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 got %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "internal server error") {
		t.Fatalf("expected generic error body, got %q", body)
	}
	if strings.Contains(body, "db timeout") {
		t.Fatalf("expected internal error to be hidden, got %q", body)
	}
}

// TestEnergySummaryComputesSyntheticOnTheFly is the integration
// test for the on-the-fly energy-flow pipeline. The store returns
// raw Modbus accumulator rows (no synthetic `*_to_*_kwh` rows
// persisted anywhere), and the handler must run
// energyflow.Recompute internally to fill resp.Flows.
//
// We pick a scenario where one minute interval has 6 kWh of PV
// yield and 5 kWh of ESS charge: with no grid import, the allocator
// must attribute the entire 5 kWh charge delta to pv_to_ess_kwh.
// That number — produced exclusively by the on-the-fly path — is
// the proof that the handler stopped reading synthetic samples
// from the store and started computing them itself.
func TestEnergySummaryComputesSyntheticOnTheFly(t *testing.T) {
	from := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	to := from.Add(2 * time.Minute)
	store := &mockStore{
		summaryResp: EnergySummaryResponse{
			OrganizationID: "org-a",
			From:           from,
			To:             to,
			Totals: map[string]float64{
				"accumulated_pv_energy_yield_kwh": 6,
			},
		},
		flowSources: []EnergyFlowRawRow{
			{Time: from, MetricKey: "accumulated_pv_energy_yield_kwh", Value: 100, DeviceHost: ""},
			{Time: from, MetricKey: "accumulated_electricity_purchased_kwh", Value: 50, DeviceHost: ""},
			{Time: from, MetricKey: "accumulated_electricity_sold_kwh", Value: 10, DeviceHost: ""},
			{Time: from, MetricKey: "total_energy_charged_kwh", Value: 20, DeviceHost: ""},
			{Time: from, MetricKey: "total_energy_discharged_kwh", Value: 5, DeviceHost: ""},
			{Time: from.Add(time.Minute), MetricKey: "accumulated_pv_energy_yield_kwh", Value: 106, DeviceHost: ""},
			{Time: from.Add(time.Minute), MetricKey: "accumulated_electricity_purchased_kwh", Value: 50, DeviceHost: ""},
			{Time: from.Add(time.Minute), MetricKey: "accumulated_electricity_sold_kwh", Value: 10, DeviceHost: ""},
			{Time: from.Add(time.Minute), MetricKey: "total_energy_charged_kwh", Value: 25, DeviceHost: ""},
			{Time: from.Add(time.Minute), MetricKey: "total_energy_discharged_kwh", Value: 5, DeviceHost: ""},
		},
	}
	h := NewHandlers(store, "*")
	h.SetEnergyFlowOrgs([]EnergyFlowOrg{{ID: "org-a"}})

	url := fmt.Sprintf("/api/v1/energy-summary?organization_id=org-a&metric_keys=pv_to_ess_kwh,grid_to_ess_kwh,ess_to_load_kwh,ess_to_grid_kwh&from=%s&to=%s",
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp EnergySummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Flows == nil {
		t.Fatalf("resp.Flows is nil, want populated by on-the-fly compute")
	}
	if resp.Flows.PVToESSKwh <= 0 {
		t.Errorf("flows.pv_to_ess_kwh = %g, want > 0 (computed on the fly)", resp.Flows.PVToESSKwh)
	}
	if _, ok := resp.Totals["pv_to_ess_kwh"]; ok {
		t.Errorf("synthetic key leaked into resp.Totals; should only appear in resp.Flows")
	}
	if store.flowSourcesOrg != "org-a" {
		t.Errorf("EnergyFlowSources not called with the right org; got %q", store.flowSourcesOrg)
	}
}

// TestEnergySummaryNoFlowComputeWhenSyntheticNotRequested verifies
// the fast path: if the caller never asks for a synthetic key the
// handler must NOT pay the cost of pulling raw rows and re-running
// the allocator, and resp.Flows must come back nil so the client
// can distinguish "no compute" from "computed and got zero".
func TestEnergySummaryNoFlowComputeWhenSyntheticNotRequested(t *testing.T) {
	from := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	store := &mockStore{
		summaryResp: EnergySummaryResponse{
			OrganizationID: "org-a",
			From:           from,
			To:             to,
			Totals:         map[string]float64{"accumulated_pv_energy_yield_kwh": 7},
		},
	}
	h := NewHandlers(store, "*")
	h.SetEnergyFlowOrgs([]EnergyFlowOrg{{ID: "org-a"}})

	url := fmt.Sprintf("/api/v1/energy-summary?organization_id=org-a&metric_keys=accumulated_pv_energy_yield_kwh&from=%s&to=%s",
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.flowSourcesOrg != "" {
		t.Errorf("EnergyFlowSources should not have been called, was called with %q", store.flowSourcesOrg)
	}
	var resp EnergySummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Flows != nil {
		t.Errorf("resp.Flows = %+v, want nil when synthetic keys were not requested", resp.Flows)
	}
}

// TestEnergySummaryFlowDegradesOnEmptyData covers the "no raw rows
// for the requested window" case. The on-the-fly compute returns a
// non-nil pointer with zero fields rather than failing, so a
// dashboard browsing a period before the deployment started polling
// still renders cleanly with pv_to_ess=0 etc. instead of a 500.
func TestEnergySummaryFlowDegradesOnEmptyData(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Hour)
	store := &mockStore{
		summaryResp: EnergySummaryResponse{
			OrganizationID: "org-a",
			From:           from,
			To:             to,
			Totals:         map[string]float64{},
		},
		flowSources: nil,
	}
	h := NewHandlers(store, "*")
	h.SetEnergyFlowOrgs([]EnergyFlowOrg{{ID: "org-a"}})

	url := fmt.Sprintf("/api/v1/energy-summary?organization_id=org-a&metric_keys=pv_to_ess_kwh&from=%s&to=%s",
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp EnergySummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Flows == nil {
		t.Fatalf("resp.Flows is nil, want non-nil pointer with all-zero fields")
	}
	if resp.Flows.PVToESSKwh != 0 ||
		resp.Flows.GridToESSKwh != 0 ||
		resp.Flows.ESSToLoadKwh != 0 ||
		resp.Flows.ESSToGridKwh != 0 {
		t.Errorf("resp.Flows = %+v, want all-zero struct for an empty window", resp.Flows)
	}
}

// TestEnergySummaryFlowSkipsWideWindow verifies the temporary
// day-only guard inside EnergySummary: a request whose window is
// wider than `maxEnergyFlowWindow` must NOT pull raw rows or run
// the allocator, and resp.Flows must come back nil so the client
// knows the compute was skipped. This keeps month/year refreshes
// from blocking on a multi-second allocator pass while we ship a
// proper daily-rollup cache.
func TestEnergySummaryFlowSkipsWideWindow(t *testing.T) {
	from := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{
		summaryResp: EnergySummaryResponse{
			OrganizationID: "org-a",
			From:           from,
			To:             to,
			Totals:         map[string]float64{"accumulated_pv_energy_yield_kwh": 12345},
		},
	}
	h := NewHandlers(store, "*")
	h.SetEnergyFlowOrgs([]EnergyFlowOrg{{ID: "org-a"}})

	url := fmt.Sprintf("/api/v1/energy-summary?organization_id=org-a&metric_keys=accumulated_pv_energy_yield_kwh,pv_to_ess_kwh&from=%s&to=%s",
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	req := httptest.NewRequest(http.MethodGet, url, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.flowSourcesOrg != "" {
		t.Errorf("EnergyFlowSources should not have been called for a wide window, was called with %q", store.flowSourcesOrg)
	}
	var resp EnergySummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Flows != nil {
		t.Errorf("resp.Flows = %+v, want nil for skipped wide window", resp.Flows)
	}
	if got := resp.Totals["accumulated_pv_energy_yield_kwh"]; got != 12345 {
		t.Errorf("raw counter total clobbered: got %g, want 12345", got)
	}
}

// TestEnergyFlowRecomputeEndpointGone is a tiny regression guard
// that future re-introductions of the persisted-cumulative pipeline
// won't accidentally bring the dead /energy-flow/recompute route
// back without a fresh design review.
func TestEnergyFlowRecomputeEndpointGone(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodOptions} {
		req := httptest.NewRequest(method, "/api/v1/energy-flow/recompute?organization_id=org-a&from=2026-05-01T00:00:00Z&to=2026-05-01T01:00:00Z", nil)
		rec := httptest.NewRecorder()
		h.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusNoContent {
			// 404 is what we want; 204 is the CORS preflight
			// shortcut wrapping every unknown route in withCORS.
			t.Errorf("method=%s want 404/204 got %d body=%s", method, rec.Code, rec.Body.String())
		}
	}
}
