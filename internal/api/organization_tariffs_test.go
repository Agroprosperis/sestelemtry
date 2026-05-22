package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func validTariffsPayload() OrgTariffs {
	return OrgTariffs{
		DistributionUahPerKwh:   1.5,
		TransmissionUahPerKwh:   0.45,
		SupplierMarginUahPerKwh: 0.15,
		OtherFeesUahPerKwh:      0.05,
		ExportDiscount:          0.1,
		DegradationUahPerKwh:    0.2,
		IncludeVat:              true,
		VatRate:                 0.2,
		EssCapacityKwh:          400,
	}
}

func TestOrganizationTariffsRejectsUnsupportedMethods(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	for _, method := range []string{http.MethodPost, http.MethodDelete, http.MethodPatch} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/organization-tariffs?organization_id=ze", nil)
			rec := httptest.NewRecorder()
			h.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("want 405 got %d body=%s", rec.Code, rec.Body.String())
			}
			allow := rec.Header().Get("Allow")
			if !strings.Contains(allow, "GET") || !strings.Contains(allow, "PUT") {
				t.Fatalf("Allow header missing GET/PUT: %q", allow)
			}
		})
	}
}

func TestOrganizationTariffsGetRequiresOrgID(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organization-tariffs", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestOrganizationTariffsGetReturns404WhenMissing(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organization-tariffs?organization_id=ze", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOrganizationTariffsGetReturnsStored(t *testing.T) {
	stored := validTariffsPayload()
	store := &mockStore{tariffsByOrg: map[string]OrgTariffs{"ze": stored}}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organization-tariffs?organization_id=ze", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d body=%s", rec.Code, rec.Body.String())
	}
	var got OrgTariffs
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got != stored {
		t.Fatalf("payload mismatch: got=%+v want=%+v", got, stored)
	}
}

func TestOrganizationTariffsGetHidesInternalError(t *testing.T) {
	store := &mockStore{tariffsGetErr: errors.New("db down")}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/organization-tariffs?organization_id=ze", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Fatalf("expected generic error body, got %q", rec.Body.String())
	}
}

func TestOrganizationTariffsPutRoundTrip(t *testing.T) {
	store := &mockStore{}
	h := NewHandlers(store, "*")
	body, _ := json.Marshal(validTariffsPayload())
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/organization-tariffs?organization_id=ze", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("want 204 got %d body=%s", rec.Code, rec.Body.String())
	}
	if store.tariffsLastOrg != "ze" {
		t.Fatalf("organization_id not propagated: %q", store.tariffsLastOrg)
	}
	if store.tariffsLastWrite != validTariffsPayload() {
		t.Fatalf("payload mismatch: got=%+v", store.tariffsLastWrite)
	}

	// Now the GET path should echo the same payload from the same
	// mock store, exercising the round-trip end to end.
	getReq := httptest.NewRequest(http.MethodGet,
		"/api/v1/organization-tariffs?organization_id=ze", nil)
	getRec := httptest.NewRecorder()
	h.Router().ServeHTTP(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("round-trip GET want 200 got %d body=%s", getRec.Code, getRec.Body.String())
	}
}

func TestOrganizationTariffsPutRequiresOrgID(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	body, _ := json.Marshal(validTariffsPayload())
	req := httptest.NewRequest(http.MethodPut, "/api/v1/organization-tariffs", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d", rec.Code)
	}
}

func TestOrganizationTariffsPutRejectsUnknownFields(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	// Mix in a field the DTO doesn't define so DisallowUnknownFields
	// kicks the body back as a 400 — keeps a frontend that drifts
	// from the API contract from silently dropping data on disk.
	body := []byte(`{"distribution_uah_per_kwh":1,"vat_rate":0.2,"ess_capacity_kwh":400,"mystery":42}`)
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/organization-tariffs?organization_id=ze", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestOrganizationTariffsPutValidationErrors(t *testing.T) {
	cases := []struct {
		name     string
		mutate   func(*OrgTariffs)
		wantText string
	}{
		{"vat too high", func(t *OrgTariffs) { t.VatRate = 1.5 }, "vat_rate"},
		{"vat negative", func(t *OrgTariffs) { t.VatRate = -0.1 }, "vat_rate"},
		{"export discount > 1", func(t *OrgTariffs) { t.ExportDiscount = 1.5 }, "export_discount"},
		{"capacity zero", func(t *OrgTariffs) { t.EssCapacityKwh = 0 }, "ess_capacity_kwh"},
		{"capacity negative", func(t *OrgTariffs) { t.EssCapacityKwh = -1 }, "ess_capacity_kwh"},
		{"distribution negative", func(t *OrgTariffs) { t.DistributionUahPerKwh = -1 }, "distribution_uah_per_kwh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := validTariffsPayload()
			tc.mutate(&payload)
			body, _ := json.Marshal(payload)
			h := NewHandlers(&mockStore{}, "*")
			req := httptest.NewRequest(http.MethodPut,
				"/api/v1/organization-tariffs?organization_id=ze", bytes.NewReader(body))
			rec := httptest.NewRecorder()
			h.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400 got %d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantText) {
				t.Fatalf("expected error to mention %q, got %q", tc.wantText, rec.Body.String())
			}
		})
	}
}

func TestOrganizationTariffsPutHidesInternalError(t *testing.T) {
	store := &mockStore{tariffsPutErr: errors.New("db down")}
	h := NewHandlers(store, "*")
	body, _ := json.Marshal(validTariffsPayload())
	req := httptest.NewRequest(http.MethodPut,
		"/api/v1/organization-tariffs?organization_id=ze", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500 got %d", rec.Code)
	}
}
