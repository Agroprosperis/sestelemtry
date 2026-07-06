package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
)

// stubEconomicsBackend is a no-op economics.Backend used to install a
// configured service so handler-level validation (which runs before any
// backend call) can be exercised without a database.
type stubEconomicsBackend struct{}

func (stubEconomicsBackend) HourlyFlows(context.Context, string, time.Time) ([]economics.FlowRow, error) {
	return nil, nil
}
func (stubEconomicsBackend) Timeseries(context.Context, string, []string, time.Time, time.Time, string, string, string) ([]economics.Point, error) {
	return nil, nil
}
func (stubEconomicsBackend) DAMPrices(context.Context, int, time.Time, time.Time) ([]economics.DAMHour, error) {
	return nil, nil
}
func (stubEconomicsBackend) TariffSchedule(context.Context, string) (economics.Schedule, error) {
	return nil, nil
}
func (stubEconomicsBackend) CanonicalDaily(context.Context, string, time.Time) (economics.CanonicalDaily, bool, error) {
	return economics.CanonicalDaily{}, false, nil
}
func (stubEconomicsBackend) SaveDay(context.Context, economics.StoredDay) error { return nil }
func (stubEconomicsBackend) LoadDay(context.Context, string, time.Time, string) (economics.StoredDay, bool, error) {
	return economics.StoredDay{}, false, nil
}
func (stubEconomicsBackend) LoadDailyRange(context.Context, string, time.Time, time.Time) ([]economics.DailyRecord, error) {
	return nil, nil
}
func (stubEconomicsBackend) LoadHourlyRange(context.Context, string, time.Time, time.Time) ([]economics.HourlyRecord, error) {
	return nil, nil
}

func TestEconomicsDailyUnconfigured(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/economics/daily?organization_id=org-a&date=2026-04-01", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when economics service not wired, got %d", rec.Code)
	}
}

func TestEconomicsMonthlyUnconfigured(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/economics/monthly?organization_id=org-a&month=2026-06", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when economics service not wired, got %d", rec.Code)
	}
}

func TestEconomicsMonthlyBadMonth(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	h.SetEconomicsService(economics.NewService(stubEconomicsBackend{}))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/economics/monthly?organization_id=org-a&month=2026-13", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for malformed month, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestEconomicsRecomputeUnconfigured(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/economics/recompute?organization_id=org-a&from=2026-04-01&to=2026-04-02", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when economics service not wired, got %d", rec.Code)
	}
}

func TestEconomicsDataRangeHasData(t *testing.T) {
	store := &mockStore{
		dataRangeOK:  true,
		dataRangeMin: time.Date(2026, 1, 15, 10, 0, 0, 0, time.UTC),
		dataRangeMax: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC),
	}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/economics/data-range?organization_id=org-a&tz=Europe/Kyiv", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{`"from":"2026-01-15"`, `"to":"2026-07-05"`, `"has_data":true`} {
		if !strings.Contains(body, want) {
			t.Errorf("expected body to contain %s, got %s", want, body)
		}
	}
}

func TestEconomicsDataRangeNoData(t *testing.T) {
	h := NewHandlers(&mockStore{dataRangeOK: false}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/economics/data-range?organization_id=org-a", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"has_data":false`) {
		t.Errorf("expected has_data:false, got %s", rec.Body.String())
	}
}

func TestEconomicsDataRangeMissingOrg(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/economics/data-range", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing organization_id, got %d", rec.Code)
	}
}

func TestTariffScheduleGetEmpty(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organization-tariff-schedule?organization_id=org-a", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"versions"`) {
		t.Errorf("expected versions key, got %s", rec.Body.String())
	}
}

func TestTariffSchedulePutValid(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	body := `{"effective_from":"2026-04-01","tariffs":{"distribution_uah_per_kwh":1,"transmission_uah_per_kwh":0.5,"supplier_margin_uah_per_kwh":0,"other_fees_uah_per_kwh":0,"export_discount":0.05,"degradation_uah_per_kwh":0.6,"include_vat":false,"vat_rate":0.2,"ess_capacity_kwh":215}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/organization-tariff-schedule?organization_id=org-a", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestTariffSchedulePutBadDate(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	body := `{"effective_from":"nope","tariffs":{"distribution_uah_per_kwh":1,"transmission_uah_per_kwh":0.5,"supplier_margin_uah_per_kwh":0,"other_fees_uah_per_kwh":0,"export_discount":0.05,"degradation_uah_per_kwh":0.6,"include_vat":false,"vat_rate":0.2,"ess_capacity_kwh":215}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/organization-tariff-schedule?organization_id=org-a", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestTariffScheduleDeleteNotFound(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/organization-tariff-schedule?organization_id=org-a&effective_from=2026-04-01", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 (mock deletes 0 rows), got %d", rec.Code)
	}
}
