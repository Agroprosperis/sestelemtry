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
}

func (m *mockStore) Current(_ context.Context, _ string, _ []string) (CurrentResponse, error) {
	return m.currentResp, m.currentErr
}

func (m *mockStore) Timeseries(_ context.Context, _ string, _ []string, _, _ time.Time, _ string) (TimeseriesResponse, error) {
	return m.seriesResp, m.seriesErr
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
