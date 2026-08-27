package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/alerts"
)

type mockStore struct {
	currentResp  CurrentResponse
	currentErr   error
	seriesResp   TimeseriesResponse
	seriesErr    error
	summaryResp  EnergySummaryResponse
	summaryErr   error
	damResp      DAMPricesResponse
	damErr       error
	weatherResp  WeatherForecastResponse
	weatherErr   error
	inventory    *PlantInventoryResponse
	inventoryErr error
	inventoryHistory    *PlantInventoryHistoryResponse
	inventoryHistoryErr error
	readyErr     error

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

	flowDailyTotals  EnergyFlowTotals
	flowDailyCovered int
	flowDailyErr     error
	flowDailyOrg     string
	flowDailyFromDay time.Time
	flowDailyToDay   time.Time

	pvPlanDays     []PvPlanDayTotal
	pvPlanErr      error
	pvPlanOrg      string
	pvPlanFromDay  time.Time
	pvPlanToDay    time.Time
	pvPlanSaved    []PvPlanDayTotal
	pvPlanSavedOrg string
	pvPlanSaveErr  error

	tariffsByOrg     map[string]OrgTariffs
	tariffsGetErr    error
	tariffsPutErr    error
	tariffsLastOrg   string
	tariffsLastWrite OrgTariffs

	dataRangeMin time.Time
	dataRangeMax time.Time
	dataRangeOK  bool
	dataRangeErr error

	alertSettings         *alerts.Settings
	alertPasswordSet      bool
	alertSettingsGetErr   error
	alertSettingsPutErr   error
	alertSettingsWrite    *alerts.Settings
	alertPasswordWrite    *string
	alertPasswordWriteSet bool
	alertPassword         string
	orgAlertSettings      map[string]alerts.OrgSettings
	orgAlertSettingsErr   error
	orgAlertLastOrg       string
	orgAlertLastWrite     alerts.OrgSettings
}

func (m *mockStore) GetAlertSettings(_ context.Context) (alerts.Settings, bool, bool, error) {
	if m.alertSettingsGetErr != nil {
		return alerts.Settings{}, false, false, m.alertSettingsGetErr
	}
	if m.alertSettings == nil {
		return alerts.Settings{}, false, false, nil
	}
	return *m.alertSettings, m.alertPasswordSet, true, nil
}

func (m *mockStore) AlertSMTPPassword(_ context.Context) (string, error) {
	return m.alertPassword, nil
}

func (m *mockStore) UpsertAlertSettings(_ context.Context, settings alerts.Settings, password *string) error {
	if m.alertSettingsPutErr != nil {
		return m.alertSettingsPutErr
	}
	m.alertSettingsWrite = &settings
	m.alertPasswordWrite = password
	m.alertPasswordWriteSet = true
	m.alertSettings = &settings
	return nil
}

func (m *mockStore) LoadOrgAlertSettings(_ context.Context) (map[string]alerts.OrgSettings, error) {
	if m.orgAlertSettingsErr != nil {
		return nil, m.orgAlertSettingsErr
	}
	return m.orgAlertSettings, nil
}

func (m *mockStore) UpsertOrgAlertSettings(_ context.Context, organizationID string, settings alerts.OrgSettings) error {
	if m.orgAlertSettingsErr != nil {
		return m.orgAlertSettingsErr
	}
	m.orgAlertLastOrg = organizationID
	m.orgAlertLastWrite = settings
	if m.orgAlertSettings == nil {
		m.orgAlertSettings = make(map[string]alerts.OrgSettings)
	}
	m.orgAlertSettings[organizationID] = settings
	return nil
}

func (m *mockStore) TelemetryDataRange(_ context.Context, _ string) (time.Time, time.Time, bool, error) {
	return m.dataRangeMin, m.dataRangeMax, m.dataRangeOK, m.dataRangeErr
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
		if limit > 0 && emitted >= limit {
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

func (m *mockStore) EnergyFlowDailyTotals(_ context.Context, orgID string, fromDay, toDay time.Time) (EnergyFlowTotals, int, error) {
	m.flowDailyOrg = orgID
	m.flowDailyFromDay = fromDay
	m.flowDailyToDay = toDay
	if m.flowDailyErr != nil {
		return EnergyFlowTotals{}, 0, m.flowDailyErr
	}
	return m.flowDailyTotals, m.flowDailyCovered, nil
}

func (m *mockStore) PvPlanDays(_ context.Context, orgID string, fromDay, toDay time.Time) ([]PvPlanDayTotal, error) {
	m.pvPlanOrg = orgID
	m.pvPlanFromDay = fromDay
	m.pvPlanToDay = toDay
	if m.pvPlanErr != nil {
		return nil, m.pvPlanErr
	}
	return m.pvPlanDays, nil
}

func (m *mockStore) SavePvPlanDays(_ context.Context, orgID string, days []PvPlanDayTotal) error {
	m.pvPlanSavedOrg = orgID
	m.pvPlanSaved = append(m.pvPlanSaved, days...)
	return m.pvPlanSaveErr
}

func (m *mockStore) Ready(_ context.Context) error {
	return m.readyErr
}

func (m *mockStore) GetOrgTariffs(_ context.Context, organizationID string) (OrgTariffs, bool, error) {
	if m.tariffsGetErr != nil {
		return OrgTariffs{}, false, m.tariffsGetErr
	}
	t, ok := m.tariffsByOrg[organizationID]
	return t, ok, nil
}

func (m *mockStore) UpsertOrgTariffs(_ context.Context, organizationID string, tariffs OrgTariffs) error {
	if m.tariffsPutErr != nil {
		return m.tariffsPutErr
	}
	m.tariffsLastOrg = organizationID
	m.tariffsLastWrite = tariffs
	if m.tariffsByOrg == nil {
		m.tariffsByOrg = make(map[string]OrgTariffs)
	}
	m.tariffsByOrg[organizationID] = tariffs
	return nil
}

func (m *mockStore) GetTariffScheduleVersions(_ context.Context, _ string) ([]TariffScheduleVersion, error) {
	return nil, nil
}

func (m *mockStore) UpsertTariffScheduleVersion(_ context.Context, _ string, _ time.Time, _ OrgTariffs) error {
	return nil
}

func (m *mockStore) DeleteTariffScheduleVersion(_ context.Context, _ string, _ time.Time) (int64, error) {
	return 0, nil
}

func (m *mockStore) LatestPlantInventory(_ context.Context, organizationID string) (PlantInventoryResponse, bool, error) {
	if m.inventoryErr != nil {
		return PlantInventoryResponse{}, false, m.inventoryErr
	}
	if m.inventory == nil {
		return PlantInventoryResponse{}, false, nil
	}
	out := *m.inventory
	out.OrganizationID = organizationID
	return out, true, nil
}

func (m *mockStore) PlantInventoryHistory(_ context.Context, organizationID string, _ int) (PlantInventoryHistoryResponse, error) {
	if m.inventoryHistoryErr != nil {
		return PlantInventoryHistoryResponse{}, m.inventoryHistoryErr
	}
	if m.inventoryHistory == nil {
		return PlantInventoryHistoryResponse{
			OrganizationID: organizationID,
			Changes:        map[string][]PlantInventoryChange{},
		}, nil
	}
	out := *m.inventoryHistory
	out.OrganizationID = organizationID
	return out, nil
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

func TestDAMPricesRefreshRejectsNonPost(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetDAMFetcher(func(_ context.Context, _ time.Time, _ int) (int, error) {
		return 24, nil
	}, 2)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dam-prices/refresh?date=2026-05-25", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDAMPricesRefreshRequiresDate(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetDAMFetcher(func(_ context.Context, _ time.Time, _ int) (int, error) {
		t.Fatal("fetcher should not be invoked when date is missing")
		return 0, nil
	}, 2)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dam-prices/refresh", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDAMPricesRefreshRejectsBadDate(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetDAMFetcher(func(_ context.Context, _ time.Time, _ int) (int, error) {
		t.Fatal("fetcher should not be invoked on malformed date")
		return 0, nil
	}, 2)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dam-prices/refresh?date=not-a-date", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestDAMPricesRefreshUnconfigured(t *testing.T) {
	// Fetcher is nil — simulates an API process that started
	// without an `oree:` config block. The handler must respond
	// 503 with a clear "not configured" message so the operator
	// knows to point the deployment at the OREE upstream.
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dam-prices/refresh?date=2026-05-25", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "dam refresh not configured") {
		t.Fatalf("expected configuration hint in body, got %q", rec.Body.String())
	}
}

func TestDAMPricesRefreshUpstreamFailure(t *testing.T) {
	// Fetcher fails — e.g. OREE returned 502, network glitch, or
	// the parser rejected a malformed XLS. The handler maps that
	// to 502 (the upstream is the problem, not us) and returns
	// the underlying err.Error() in the body so the operator can
	// see the cause without grepping API logs.
	h := NewHandlers(&mockStore{}, "*")
	h.SetDAMFetcher(func(_ context.Context, _ time.Time, _ int) (int, error) {
		return 0, errors.New("oree: status 502")
	}, 2)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dam-prices/refresh?date=2026-05-25", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("want 502 got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "oree: status 502") {
		t.Fatalf("expected upstream err in body, got %q", rec.Body.String())
	}
}

func TestDAMPricesRefreshSuccessReturnsRereadPrices(t *testing.T) {
	// After a successful fetch the handler re-reads the day via
	// store.DAMPrices and returns that response so the frontend
	// can drop the body straight into its price map without a
	// second round-trip. We verify both the zone propagation and
	// the readback shape.
	price := 4200.0
	store := &mockStore{
		damResp: DAMPricesResponse{
			Zone: 2,
			Prices: []DAMPrice{{
				DeliveryDate:   time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC),
				Hour:           3,
				Zone:           2,
				PriceUAHPerMWh: &price,
			}},
		},
	}
	h := NewHandlers(store, "*")
	var fetchedZone int
	var fetchedDate time.Time
	h.SetDAMFetcher(func(_ context.Context, d time.Time, z int) (int, error) {
		fetchedZone, fetchedDate = z, d
		return 24, nil
	}, 2)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dam-prices/refresh?date=2026-05-25", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if fetchedZone != 2 {
		t.Fatalf("expected default zone=2, got %d", fetchedZone)
	}
	wantDate := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	if !fetchedDate.Equal(wantDate) {
		t.Fatalf("fetched date mismatch: got %v want %v", fetchedDate, wantDate)
	}
	if !store.damFrom.Equal(wantDate) || !store.damTo.Equal(wantDate) {
		t.Fatalf("readback date mismatch: from=%v to=%v", store.damFrom, store.damTo)
	}
	var got DAMPricesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Prices) != 1 || got.Prices[0].Hour != 3 || got.Prices[0].PriceUAHPerMWh == nil || *got.Prices[0].PriceUAHPerMWh != 4200.0 {
		t.Fatalf("unexpected payload: %#v", got)
	}
}

func TestDAMPricesRefreshUsesExplicitZone(t *testing.T) {
	// `zone=1` overrides the configured default; the fetcher
	// receives the requested zone and the readback uses it too.
	store := &mockStore{}
	h := NewHandlers(store, "*")
	var fetchedZone int
	h.SetDAMFetcher(func(_ context.Context, _ time.Time, z int) (int, error) {
		fetchedZone = z
		return 0, nil
	}, 2)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/dam-prices/refresh?date=2026-05-25&zone=1", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if fetchedZone != 1 {
		t.Fatalf("expected zone=1 from query, got %d", fetchedZone)
	}
	if store.damZone != 1 {
		t.Fatalf("readback should use zone=1, got %d", store.damZone)
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
	if store.samplesLimit != 0 {
		t.Fatalf("omitted limit should stream unlimited (0), got %d", store.samplesLimit)
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
			name: "limit not positive",
			url:  "/api/v1/samples?organization_id=org-a&metric_keys=soc_percent&from=2026-05-09T00:00:00Z&to=2026-05-10T00:00:00Z&limit=0",
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

// TestEnergySummaryFlowWideWindowUsesDailyCache pins the month/year
// path: a window wider than `maxEnergyFlowWindow` must never pull raw
// rows (a live allocator pass over a month takes minutes and would
// block the API worker on every dashboard refresh) and instead sums
// the per-day totals the economics daemon already persisted. The civil
// days are derived in the request's tz, and `to` is exclusive, so a
// month asked for in Kyiv time covers Apr 1..Apr 30 and not May 1.
func TestEnergySummaryFlowWideWindowUsesDailyCache(t *testing.T) {
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	from := time.Date(2026, 4, 1, 0, 0, 0, 0, kyiv)
	to := time.Date(2026, 5, 1, 0, 0, 0, 0, kyiv)
	store := &mockStore{
		summaryResp: EnergySummaryResponse{
			OrganizationID: "org-a",
			From:           from,
			To:             to,
			Totals:         map[string]float64{"accumulated_pv_energy_yield_kwh": 12345},
		},
		flowDailyTotals: EnergyFlowTotals{
			PVToESSKwh:   4000,
			GridToESSKwh: 250,
			ESSToLoadKwh: 3900,
			ESSToGridKwh: 60,
		},
		flowDailyCovered: 30,
	}
	h := NewHandlers(store, "*")
	h.SetEnergyFlowOrgs([]EnergyFlowOrg{{ID: "org-a"}})

	// Kyiv timestamps carry a `+03:00` offset, which a raw query string
	// would decode as a space.
	reqURL := fmt.Sprintf("/api/v1/energy-summary?organization_id=org-a&metric_keys=accumulated_pv_energy_yield_kwh,pv_to_ess_kwh&tz=Europe/Kyiv&from=%s&to=%s",
		neturl.QueryEscape(from.Format(time.RFC3339)), neturl.QueryEscape(to.Format(time.RFC3339)))
	req := httptest.NewRequest(http.MethodGet, reqURL, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.flowSourcesOrg != "" {
		t.Errorf("EnergyFlowSources should not have been called for a wide window, was called with %q", store.flowSourcesOrg)
	}
	if got := store.flowDailyFromDay.Format("2006-01-02"); got != "2026-04-01" {
		t.Errorf("cache from_day = %s, want 2026-04-01", got)
	}
	if got := store.flowDailyToDay.Format("2006-01-02"); got != "2026-04-30" {
		t.Errorf("cache to_day = %s, want 2026-04-30 (to is exclusive)", got)
	}
	var resp EnergySummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Flows == nil {
		t.Fatalf("resp.Flows is nil, want the cached rollup")
	}
	if resp.Flows.PVToESSKwh != 4000 || resp.Flows.ESSToLoadKwh != 3900 {
		t.Errorf("resp.Flows = %+v, want the mocked cache sums", resp.Flows)
	}
	if resp.FlowsMeta == nil {
		t.Fatalf("resp.FlowsMeta is nil, want the daily-cache provenance")
	}
	if resp.FlowsMeta.Source != EnergyFlowSourceDailyCache {
		t.Errorf("flows_meta.source = %q, want %q", resp.FlowsMeta.Source, EnergyFlowSourceDailyCache)
	}
	if resp.FlowsMeta.DaysCovered != 30 || resp.FlowsMeta.DaysExpected != 30 {
		t.Errorf("flows_meta coverage = %d/%d, want 30/30", resp.FlowsMeta.DaysCovered, resp.FlowsMeta.DaysExpected)
	}
	if got := resp.Totals["accumulated_pv_energy_yield_kwh"]; got != 12345 {
		t.Errorf("raw counter total clobbered: got %g, want 12345", got)
	}
}

// TestEnergySummaryFlowWideWindowWithoutCachedDays covers a period
// nobody has computed yet. Flows must stay nil rather than becoming an
// all-zero struct: zeros would claim the month moved no energy, which
// is a different statement from "not computed".
func TestEnergySummaryFlowWideWindowWithoutCachedDays(t *testing.T) {
	from := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	store := &mockStore{
		summaryResp: EnergySummaryResponse{
			OrganizationID: "org-a",
			From:           from,
			To:             to,
			Totals:         map[string]float64{},
		},
		flowDailyCovered: 0,
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
	if resp.Flows != nil {
		t.Errorf("resp.Flows = %+v, want nil when no day is cached", resp.Flows)
	}
}

// TestCivilDaySpan nails the two boundaries that silently corrupt a
// period total: an exclusive `to` must not reach into the next day,
// and a DST shift inside the window must not shorten the day count
// (the elapsed duration is 23 or 25 hours off a whole number of days
// across a transition).
func TestCivilDaySpan(t *testing.T) {
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	tests := []struct {
		name      string
		from, to  time.Time
		loc       *time.Location
		wantFirst string
		wantLast  string
		wantDays  int
	}{
		{
			name:      "month ending at midnight stops on the last day",
			from:      time.Date(2026, 4, 1, 0, 0, 0, 0, kyiv),
			to:        time.Date(2026, 5, 1, 0, 0, 0, 0, kyiv),
			loc:       kyiv,
			wantFirst: "2026-04-01",
			wantLast:  "2026-04-30",
			wantDays:  30,
		},
		{
			name:      "spring-forward month still counts every day",
			from:      time.Date(2026, 3, 1, 0, 0, 0, 0, kyiv),
			to:        time.Date(2026, 4, 1, 0, 0, 0, 0, kyiv),
			loc:       kyiv,
			wantFirst: "2026-03-01",
			wantLast:  "2026-03-31",
			wantDays:  31,
		},
		{
			name:      "utc instants map onto local days",
			from:      time.Date(2026, 7, 31, 21, 0, 0, 0, time.UTC),
			to:        time.Date(2026, 8, 31, 21, 0, 0, 0, time.UTC),
			loc:       kyiv,
			wantFirst: "2026-08-01",
			wantLast:  "2026-08-31",
			wantDays:  31,
		},
		{
			name:      "partial current day is one day",
			from:      time.Date(2026, 8, 1, 0, 0, 0, 0, kyiv),
			to:        time.Date(2026, 8, 1, 13, 30, 0, 0, kyiv),
			loc:       kyiv,
			wantFirst: "2026-08-01",
			wantLast:  "2026-08-01",
			wantDays:  1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			first, last, days := civilDaySpan(tc.from, tc.to, tc.loc)
			if got := first.Format("2006-01-02"); got != tc.wantFirst {
				t.Errorf("first = %s, want %s", got, tc.wantFirst)
			}
			if got := last.Format("2006-01-02"); got != tc.wantLast {
				t.Errorf("last = %s, want %s", got, tc.wantLast)
			}
			if days != tc.wantDays {
				t.Errorf("days = %d, want %d", days, tc.wantDays)
			}
		})
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
