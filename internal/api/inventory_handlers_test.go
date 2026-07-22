package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPlantInventoryOK(t *testing.T) {
	pv := 450.0
	store := &mockStore{
		inventory: &PlantInventoryResponse{
			Time:         time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
			PollReason:   "startup",
			PVRatedKw:    &pv,
			QualityFlags: []string{},
		},
	}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plant-inventory?organization_id=ab", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got PlantInventoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OrganizationID != "ab" || got.PVRatedKw == nil || *got.PVRatedKw != 450 {
		t.Fatalf("got=%+v", got)
	}
}

func TestPlantInventoryNotFound(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plant-inventory?organization_id=ab", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestPlantInventoryHistoryOK(t *testing.T) {
	from, to := 450.0, 600.0
	store := &mockStore{
		inventoryHistory: &PlantInventoryHistoryResponse{
			Changes: map[string][]PlantInventoryChange{
				"pv_rated_kw": {{
					At:   time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
					From: &from,
					To:   &to,
				}},
			},
		},
	}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/plant-inventory/history?organization_id=ab", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var got PlantInventoryHistoryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.OrganizationID != "ab" {
		t.Fatalf("org=%q", got.OrganizationID)
	}
	ev := got.Changes["pv_rated_kw"]
	if len(ev) != 1 || ev[0].From == nil || *ev[0].From != 450 {
		t.Fatalf("changes=%#v", got.Changes)
	}
}
