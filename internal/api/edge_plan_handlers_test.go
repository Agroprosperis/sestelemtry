package api

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
)

func TestEdgePlannerEndpointsGuardRails(t *testing.T) {
	// Unconfigured service → 503 on every planner endpoint.
	h := edgeTestHandlers(t, nil)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/edge/sites"},
		{http.MethodGet, "/api/v1/edge/load-plan?site_id=ab"},
		{http.MethodPost, "/api/v1/edge/plan/preview?site_id=ab"},
		{http.MethodGet, "/api/v1/edge/manifests?site_id=ab"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("{}"))
		rec := httptest.NewRecorder()
		h.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status = %d, want 503", tc.method, tc.path, rec.Code)
		}
	}

	h = edgeTestHandlers(t, &EdgeIngest{Tokens: map[string]string{"ab": "tok"}})

	// Unknown site → 404 (checked before any DB access).
	req := httptest.NewRequest(http.MethodGet, "/api/v1/edge/load-plan?site_id=zz", nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: status = %d, want 404", rec.Code)
	}

	// Missing site_id → 400.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/edge/manifests", nil)
	rec = httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("no site_id: status = %d, want 400", rec.Code)
	}

	// Wrong method → 405.
	req = httptest.NewRequest(http.MethodPost, "/api/v1/edge/manifests?site_id=ab", nil)
	rec = httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST manifests: status = %d, want 405", rec.Code)
	}

	// Sites list works without a DB.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/edge/sites", nil)
	rec = httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ab"`) {
		t.Errorf("sites: status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// planPreviewFixture builds inputs for a two-day horizon: today 20:00 —
// tomorrow 24:00 (UTC as the planner timezone keeps the math readable).
func planPreviewFixture(t *testing.T) (edgePlanInputs, []economics.ForwardStep) {
	t.Helper()
	loc := time.UTC
	now := time.Date(2026, 8, 13, 20, 30, 0, 0, loc)
	start := now.Truncate(time.Hour)
	end := time.Date(2026, 8, 15, 0, 0, 0, 0, loc)

	in := edgePlanInputs{
		Loc:          loc,
		Timezone:     "UTC",
		Now:          now,
		Start:        start,
		End:          end,
		Tariffs:      economics.Tariffs{DegradationUahPerKwh: 0.5},
		CapacityKwh:  400,
		PowerKw:      200,
		SocMin:       20,
		SocMax:       90,
		StartSoc:     50,
		OperatorHour: map[time.Time]bool{},
	}
	cheap, expensive := 1.0, 10.0
	for ts := start; ts.Before(end); ts = ts.Add(time.Hour) {
		fh := economics.ForwardHour{TS: ts, LoadKw: 100}
		h := ts.Hour()
		switch {
		case h >= 1 && h <= 5:
			fh.RdnUahPerKwh = &cheap
		case h >= 19 && h <= 21:
			fh.RdnUahPerKwh = &expensive
		default:
			mid := 4.0
			fh.RdnUahPerKwh = &mid
		}
		in.Hours = append(in.Hours, fh)
	}
	steps, err := economics.BuildForwardPlan(in.Hours, economics.ForwardParams{
		Tariffs:     in.Tariffs,
		CapacityKwh: in.CapacityKwh,
		PowerKw:     in.PowerKw,
		SocMinPct:   in.SocMin,
		SocMaxPct:   in.SocMax,
		StartSocPct: in.StartSoc,
	})
	if err != nil {
		t.Fatal(err)
	}
	return in, steps
}

func TestBuildEdgePlanPreviewShape(t *testing.T) {
	in, steps := planPreviewFixture(t)
	resp := buildEdgePlanPreview("ab", in, steps)

	if len(resp.Hours) != len(in.Hours) {
		t.Fatalf("hours = %d, want %d", len(resp.Hours), len(in.Hours))
	}
	if got := resp.TomorrowStart; !got.Equal(time.Date(2026, 8, 14, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("tomorrow_start = %v", got)
	}
	// 20:00 today is not tomorrow; 00:00 next day is.
	if resp.Hours[0].Tomorrow {
		t.Error("first hour marked tomorrow")
	}
	if !resp.Hours[4].Tomorrow {
		t.Errorf("hour %v not marked tomorrow", resp.Hours[4].TS)
	}
	for _, hr := range resp.Hours {
		if hr.GridKw < 0 {
			t.Errorf("%v: negative planned grid draw %v", hr.TS, hr.GridKw)
		}
		if hr.Tradable && hr.ImportUah <= 0 {
			t.Errorf("%v: tradable hour without an import price", hr.TS)
		}
	}
	if len(resp.Days) != 2 {
		t.Fatalf("days = %d, want 2", len(resp.Days))
	}
	if resp.Days[0].Tomorrow || !resp.Days[1].Tomorrow {
		t.Errorf("day flags wrong: %+v / %+v", resp.Days[0], resp.Days[1])
	}
}

func TestAggregatePvPlanRows(t *testing.T) {
	rows := []map[string]any{
		// Two orientations in hour_ending 12 → summed.
		{"hour_ending": float64(12), "orientation_idx": float64(0), "planned_kwh": float64(120)},
		{"hour_ending": float64(12), "orientation_idx": float64(1), "planned_kwh": float64(80)},
		// A duplicate (hour, orientation) pair → the last record wins.
		{"hour_ending": float64(13), "orientation_idx": float64(0), "planned_kwh": float64(50)},
		{"hour_ending": float64(13), "orientation_idx": float64(0), "planned_kwh": float64(70)},
		// String-typed numbers (the webhook is not strict about types).
		{"hour_ending": "14", "orientation_idx": "1", "planned_kwh": "33.5"},
		// Garbage rows are skipped.
		{"hour_ending": float64(99), "orientation_idx": float64(0), "planned_kwh": float64(10)},
		{"hour_ending": float64(15), "orientation_idx": float64(0), "planned_kwh": "not-a-number"},
	}
	got := aggregatePvPlanRows(rows)
	if v := got[11]; v != 200 {
		t.Errorf("hour 11 = %v, want 200 (two orientations summed)", v)
	}
	if v := got[12]; v != 70 {
		t.Errorf("hour 12 = %v, want 70 (duplicate orientation, last wins)", v)
	}
	if v := got[13]; v != 33.5 {
		t.Errorf("hour 13 = %v, want 33.5 (string-typed row)", v)
	}
	if _, ok := got[98]; ok {
		t.Error("hour_ending 99 must be dropped")
	}
	if _, ok := got[14]; ok {
		t.Error("NaN planned_kwh must be dropped")
	}
}

func TestEdgeDayEffectsAccounting(t *testing.T) {
	in, steps := planPreviewFixture(t)
	resp := buildEdgePlanPreview("ab", in, steps)
	tomorrow := resp.Days[1]

	// The cheap night + expensive evening must make tomorrow's plan
	// profitable, and the identity ефект = потоки + тіньова must hold.
	if tomorrow.NetEffectUah <= 0 {
		t.Errorf("tomorrow net effect = %v, want > 0", tomorrow.NetEffectUah)
	}
	sum := tomorrow.FlowsUah + tomorrow.SocCarryUah
	if math.Abs(sum-tomorrow.NetEffectUah) > 0.2 {
		t.Errorf("net %.2f != flows %.2f + carry %.2f", tomorrow.NetEffectUah, tomorrow.FlowsUah, tomorrow.SocCarryUah)
	}
	// Flows must decompose exactly.
	dec := tomorrow.EssToLoadUah - tomorrow.GridChargeCostUah - tomorrow.PvChargeCostUah - tomorrow.DegradationUah
	if math.Abs(dec-tomorrow.FlowsUah) > 0.2 {
		t.Errorf("flows %.2f != decomposition %.2f", tomorrow.FlowsUah, dec)
	}
	// Baseline vs plan comparison is consistent by construction.
	if math.Abs((tomorrow.BaselineCostUah-tomorrow.PlanCostUah)-tomorrow.FlowsUah) > 0.2 {
		t.Errorf("baseline %.2f − plan %.2f != flows %.2f",
			tomorrow.BaselineCostUah, tomorrow.PlanCostUah, tomorrow.FlowsUah)
	}
	// Degradation charged per discharged kWh.
	wantDeg := tomorrow.EssToLoadKwh * in.Tariffs.DegradationUahPerKwh
	if math.Abs(tomorrow.DegradationUah-wantDeg) > 0.2 {
		t.Errorf("degradation = %v, want %v", tomorrow.DegradationUah, wantDeg)
	}
	// SOC continuity between the two day slices.
	if resp.Days[0].SocClosePct != resp.Days[1].SocOpenPct {
		t.Errorf("SOC not continuous across midnight: %v → %v",
			resp.Days[0].SocClosePct, resp.Days[1].SocOpenPct)
	}
}
