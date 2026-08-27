package api

import (
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/economics"
	"github.com/nesh/sestelemetry/internal/storage"
)

func TestEdgePlannerEndpointsGuardRails(t *testing.T) {
	// Unconfigured service → 503 on every planner endpoint.
	h := edgeTestHandlers(t, nil)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/edge/sites"},
		{http.MethodGet, "/api/v1/edge/load-plan?site_id=ab"},
		{http.MethodPost, "/api/v1/edge/plan/preview?site_id=ab"},
		{http.MethodGet, "/api/v1/edge/manifests?site_id=ab"},
		{http.MethodGet, "/api/v1/edge/status?site_id=ab"},
		{http.MethodGet, "/api/v1/edge/fleet"},
		{http.MethodPost, "/api/v1/edge/manifest/publish-manual?site_id=ab"},
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

	// Settings PUT rejects invalid payloads before any DB access.
	for _, tc := range []struct{ name, body string }{
		{"soc out of range", `{"soc_target_pct":120}`},
		{"reserve above target", `{"soc_target_pct":50,"soc_reserve_pct":60}`},
		{"negative power", `{"auto_charge_max_kw":-5}`},
		{"broken json", `{`},
	} {
		req = httptest.NewRequest(http.MethodPut, "/api/v1/edge/settings?site_id=ab", strings.NewReader(tc.body))
		rec = httptest.NewRecorder()
		h.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("settings PUT %s: status = %d, want 400", tc.name, rec.Code)
		}
	}
}

func TestBuildEdgeSiteStatus(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	// Empty snapshot: no heartbeat, no manifest, no decision.
	resp := buildEdgeSiteStatus(storage.EdgeSiteStatus{SiteID: "ze"}, now)
	if resp.Heartbeat.Online || resp.Heartbeat.UpdatedAt != nil {
		t.Errorf("empty: heartbeat = %+v, want offline/never-seen", resp.Heartbeat)
	}
	if resp.Manifest.State != "none" {
		t.Errorf("empty: manifest state = %q, want none", resp.Manifest.State)
	}
	if resp.Decision != nil {
		t.Errorf("empty: decision = %+v, want nil", resp.Decision)
	}

	// Fresh heartbeat + applied, still-valid manifest + decision.
	st := storage.EdgeSiteStatus{
		SiteID:             "ze",
		HeartbeatAt:        now.Add(-45 * time.Second),
		Status:             "shadow",
		ManifestID:         "ze-20260826-01",
		ManifestIssuedAt:   now.Add(-time.Hour),
		ManifestValidUntil: now.Add(time.Hour),
		ManifestAppliedAt:  now.Add(-50 * time.Minute),
		DecisionAt:         now.Add(-3 * time.Second),
		DecisionRecord:     []byte(`{"outputs":{"p_bess_virtual_kw":180}}`),
	}
	resp = buildEdgeSiteStatus(st, now)
	if !resp.Heartbeat.Online || resp.Heartbeat.AgeSeconds == nil || *resp.Heartbeat.AgeSeconds != 45 {
		t.Errorf("fresh: heartbeat = %+v, want online age 45", resp.Heartbeat)
	}
	if resp.Manifest.State != "applied" {
		t.Errorf("fresh: manifest state = %q, want applied", resp.Manifest.State)
	}
	if resp.Decision == nil || resp.Decision.AgeSeconds != 3 {
		t.Errorf("fresh: decision = %+v, want age 3", resp.Decision)
	}

	// Stale heartbeat → offline; expired manifest wins over applied.
	st.HeartbeatAt = now.Add(-10 * time.Minute)
	st.ManifestValidUntil = now.Add(-time.Minute)
	resp = buildEdgeSiteStatus(st, now)
	if resp.Heartbeat.Online {
		t.Error("stale: heartbeat online, want offline")
	}
	if resp.Manifest.State != "expired" {
		t.Errorf("stale: manifest state = %q, want expired", resp.Manifest.State)
	}

	// Published but not yet confirmed → pending.
	st.ManifestValidUntil = now.Add(time.Hour)
	st.ManifestAppliedAt = time.Time{}
	resp = buildEdgeSiteStatus(st, now)
	if resp.Manifest.State != "pending" {
		t.Errorf("unconfirmed: manifest state = %q, want pending", resp.Manifest.State)
	}
}

func TestEdgeManualPublishValidate(t *testing.T) {
	hour := time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)

	ok := edgeManualPublishRequest{
		TTLHours: 4,
		Preset:   "self_consumption_safe",
		Intervals: []edgeManualInterval{
			{TS: hour, EssKw: -150, SocTargetPct: 70},
			{TS: hour.Add(time.Hour), EssKw: 200},
		},
	}
	if err := ok.validate(); err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if got := ok.ttl(); got != 4*time.Hour {
		t.Errorf("ttl = %v, want 4h", got)
	}
	// Zero TTL → default.
	if got := (&edgeManualPublishRequest{}).ttl(); got != edgeManualTTLDefault {
		t.Errorf("default ttl = %v, want %v", got, edgeManualTTLDefault)
	}
	// Cancel skips all other validation.
	cancel := edgeManualPublishRequest{Cancel: true, TTLHours: 999}
	if err := cancel.validate(); err != nil {
		t.Errorf("cancel rejected: %v", err)
	}

	for name, req := range map[string]edgeManualPublishRequest{
		"ttl too long":   {TTLHours: 100},
		"unknown preset": {Preset: "island_mode"},
		"zero ts":        {Intervals: []edgeManualInterval{{EssKw: 10}}},
		"ess too large":  {Intervals: []edgeManualInterval{{TS: hour, EssKw: 50000}}},
		"soc range":      {Intervals: []edgeManualInterval{{TS: hour, EssKw: 10, SocTargetPct: 150}}},
		"duplicate hour": {Intervals: []edgeManualInterval{
			{TS: hour, EssKw: 10}, {TS: hour.Add(30 * time.Minute), EssKw: 20},
		}},
	} {
		if err := req.validate(); err == nil {
			t.Errorf("%s: accepted, want error", name)
		}
	}
}

func TestManualManifestGuard(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)

	manual := []byte(`{"source":"manual","valid_until":"2026-08-26T15:00:00Z"}`)
	if !manualManifestActive(manual, now) {
		t.Error("valid manual manifest not detected")
	}
	if manualManifestActive(manual, now.Add(4*time.Hour)) {
		t.Error("expired manual manifest still blocks")
	}
	auto := []byte(`{"source":"auto","valid_until":"2026-08-27T00:00:00Z"}`)
	if manualManifestActive(auto, now) {
		t.Error("auto manifest treated as manual")
	}
	legacy := []byte(`{"valid_until":"2026-08-27T00:00:00Z"}`) // pre-source payloads
	if manualManifestActive(legacy, now) {
		t.Error("legacy manifest treated as manual")
	}
	if manualManifestActive([]byte("not json"), now) {
		t.Error("garbage payload treated as manual")
	}
}

func TestEdgeManualManifestID(t *testing.T) {
	hour := time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)
	doc := edgeManifestDoc{Preset: "economic_arbitrage", ValidUntil: hour.Add(4 * time.Hour)}
	doc.Plan = &edgePlanDoc{Granularity: "1h", LoadSource: "manual",
		Intervals: []edgePlanInterval{{TS: hour, EssKw: 100}}}

	a := edgeManualManifestID("ze", doc)
	b := edgeManualManifestID("ze", doc)
	if a != b {
		t.Errorf("same content produced different ids: %s vs %s", a, b)
	}
	// A longer TTL must produce a new version even with identical hours.
	doc.ValidUntil = hour.Add(8 * time.Hour)
	if c := edgeManualManifestID("ze", doc); c == a {
		t.Error("TTL change did not change the manifest id")
	}
	if !strings.Contains(a, "-manual-") {
		t.Errorf("manual id %q lacks the manual marker", a)
	}
}

func TestEdgeSiteSettingsValidate(t *testing.T) {
	ok := EdgeSiteSettings{
		SocTargetPct: 90, SocReservePct: 30,
		AutoChargeMaxKw: 200, AutoDischargeMaxKw: 250,
		GridImportKw: 516, GridTargetKw: 480, PvRatedKw: 501,
	}
	if err := ok.validate(); err != nil {
		t.Fatalf("valid settings rejected: %v", err)
	}
	// Zero values mean "not set" and must pass.
	empty := EdgeSiteSettings{}
	if err := empty.validate(); err != nil {
		t.Fatalf("empty settings rejected: %v", err)
	}
	bad := EdgeSiteSettings{GridImportKw: math.Inf(1)}
	if err := bad.validate(); err == nil {
		t.Fatal("Inf limit accepted")
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
