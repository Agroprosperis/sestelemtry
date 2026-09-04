package api

import (
	"context"
	"testing"

	"github.com/nesh/sestelemetry/internal/economics"
)

// Diagnostics spec §4.1: the passport (prod tariff metadata) is the
// policy source; trusted SL inventory (40484 kWh, 40396 PV kW) only
// caps it; 40398 (ESS rated kW) is untrusted and must never feed power.

func f(v float64) *float64 { return &v }

func TestResolveEdgeRatingsIgnoresUntrusted40398(t *testing.T) {
	// ze live case: SL reports 1123 kW while the passport says 864.
	store := &mockStore{inventory: &PlantInventoryResponse{
		ESSRatedKw:  f(1123.2),
		ESSRatedKwh: f(1720),
		PVRatedKw:   f(600),
	}}
	h := NewHandlers(store, "*")
	cap, pow, pv, err := h.resolveEdgeRatings(context.Background(),
		"ze", economics.Tariffs{EssPowerLimitKw: 864, EssCapacityKwh: 1376})
	if err != nil {
		t.Fatal(err)
	}
	if pow != 864 {
		t.Fatalf("power = %v, want 864 (passport, not SL 40398)", pow)
	}
	if cap != 1720 {
		t.Fatalf("capacity = %v, want 1720", cap)
	}
	if pv != 600 {
		t.Fatalf("pv = %v, want 600 (trusted 40396)", pv)
	}
}

func TestResolveEdgeRatingsTrustedSLCapsPassportCapacity(t *testing.T) {
	store := &mockStore{inventory: &PlantInventoryResponse{
		ESSRatedKwh: f(1500), // SL says less than the passport 1720
	}}
	h := NewHandlers(store, "*")
	cap, pow, _, err := h.resolveEdgeRatings(context.Background(),
		"ze", economics.Tariffs{EssPowerLimitKw: 864, EssCapacityKwh: 1376})
	if err != nil {
		t.Fatal(err)
	}
	if cap != 1500 {
		t.Fatalf("capacity = %v, want 1500 (trusted SL cap)", cap)
	}
	if pow != 864 {
		t.Fatalf("power = %v, want 864", pow)
	}
}

func TestResolveEdgeRatingsPassportOnly(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	cap, pow, pv, err := h.resolveEdgeRatings(context.Background(),
		"ke", economics.Tariffs{EssPowerLimitKw: 324, EssCapacityKwh: 516})
	if err != nil {
		t.Fatal(err)
	}
	if pow != 324 || cap != 645 || pv != 0 {
		t.Fatalf("got %v/%v/%v, want 324/645/0 (usable 516 → nameplate)", pow, cap, pv)
	}
}

func TestResolveEdgeRatingsZeroSLValuesAreNotTrusted(t *testing.T) {
	// ze had 40488 = 0; the same "zero is missing" rule holds for the
	// capping registers — a zero must not zero the policy.
	store := &mockStore{inventory: &PlantInventoryResponse{
		ESSRatedKwh: f(0),
		PVRatedKw:   f(0),
	}}
	h := NewHandlers(store, "*")
	cap, pow, pv, err := h.resolveEdgeRatings(context.Background(),
		"ze", economics.Tariffs{EssPowerLimitKw: 864, EssCapacityKwh: 1376})
	if err != nil {
		t.Fatal(err)
	}
	if cap != 1720 || pow != 864 || pv != 0 {
		t.Fatalf("got %v/%v/%v, want 1720/864/0", cap, pow, pv)
	}
}

func TestResolveEdgeRatingsNoPassportFails(t *testing.T) {
	h := NewHandlers(&mockStore{}, "*")
	if _, _, _, err := h.resolveEdgeRatings(context.Background(), "xx", economics.Tariffs{}); err == nil {
		t.Fatal("missing passport must fail, not fall back to git YAML")
	}
}
