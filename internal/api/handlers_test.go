package api

import (
	"context"
	"encoding/json"
	"errors"
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
	damResp     DAMPricesResponse
	damErr      error
	readyErr    error

	damZone int
	damFrom time.Time
	damTo   time.Time
}

func (m *mockStore) Current(_ context.Context, _ string, _ []string) (CurrentResponse, error) {
	return m.currentResp, m.currentErr
}

func (m *mockStore) Timeseries(_ context.Context, _ string, _ []string, _, _ time.Time, _, _ string) (TimeseriesResponse, error) {
	return m.seriesResp, m.seriesErr
}

func (m *mockStore) DAMPrices(_ context.Context, zone int, from, to time.Time) (DAMPricesResponse, error) {
	m.damZone, m.damFrom, m.damTo = zone, from, to
	return m.damResp, m.damErr
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
