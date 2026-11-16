package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

type genPassportStore struct {
	*mockStore
	row   storage.GeneratorPassportRow
	found bool
}

func (s *genPassportStore) GeneratorPassport(context.Context, string) (storage.GeneratorPassportRow, bool, error) {
	return s.row, s.found, nil
}

func getGeneratorPassport(t *testing.T, store storeReader) GeneratorPassportResponse {
	t.Helper()
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generator?organization_id=ke", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got GeneratorPassportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestGeneratorPassportOK(t *testing.T) {
	at := time.Date(2026, 11, 16, 9, 59, 55, 0, time.UTC)
	run := time.Date(2026, 11, 15, 18, 40, 0, 0, time.UTC)
	rated, meter, alive, sw := 100.0, 1234.5, 1.0, 1.23
	got := getGeneratorPassport(t, &genPassportStore{
		mockStore: &mockStore{},
		found:     true,
		row: storage.GeneratorPassportRow{
			DeviceHost:   "10.24.40.239",
			LastSampleAt: &at,
			RatedKw:      &rated,
			MeterKwh:     &meter,
			MeterAt:      &at,
			MeterAlive:   &alive,
			SwVersion:    &sw,
			LastRunAt:    &run,
		},
	})
	if !got.HasGenerator || got.OrganizationID != "ke" || got.DeviceHost != "10.24.40.239" {
		t.Fatalf("got=%+v", got)
	}
	if got.RatedKw == nil || *got.RatedKw != 100 || got.MeterKwh == nil || *got.MeterKwh != 1234.5 {
		t.Fatalf("rating/meter=%+v", got)
	}
	if got.MeterAlive == nil || !*got.MeterAlive {
		t.Fatalf("meter_alive=%v", got.MeterAlive)
	}
	if got.LastRunAt == nil || !got.LastRunAt.Equal(run) {
		t.Fatalf("last_run_at=%v", got.LastRunAt)
	}
}

func TestGeneratorPassportWithoutEncombi(t *testing.T) {
	got := getGeneratorPassport(t, &genPassportStore{mockStore: &mockStore{}})
	if got.HasGenerator || got.DeviceHost != "" || got.RatedKw != nil {
		t.Fatalf("got=%+v", got)
	}
	// A store that cannot read the passport answers the same way.
	got = getGeneratorPassport(t, &mockStore{})
	if got.HasGenerator {
		t.Fatalf("got=%+v", got)
	}
}

func TestGeneratorPassportNeedsOrganization(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/generator", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}
