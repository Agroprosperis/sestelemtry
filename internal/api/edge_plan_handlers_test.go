package api

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

func TestEdgePlannerEndpointsGuardRails(t *testing.T) {
	// Unconfigured service → 503 on every control endpoint.
	h := edgeTestHandlers(t, nil)
	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/edge/sites"},
		{http.MethodGet, "/api/v1/edge/manifests?site_id=ab"},
		{http.MethodGet, "/api/v1/edge/status?site_id=ab"},
		{http.MethodGet, "/api/v1/edge/fleet"},
		{http.MethodPost, "/api/v1/edge/manifest/publish?site_id=ab"},
		{http.MethodGet, "/api/v1/dispatch/state?site_id=ab"},
		{http.MethodPost, "/api/v1/dispatch/preview?site_id=ab"},
		{http.MethodPost, "/api/v1/dispatch/confirm?site_id=ab"},
		{http.MethodGet, "/api/v1/dispatch/day?site_id=ab&date=2026-10-06"},
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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/dispatch/state?site_id=zz", nil)
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

	// Empty snapshot must not expose a health key at all (the UI keys
	// panel visibility on its presence).
	if resp.Health != nil {
		t.Errorf("empty: health = %s, want absent", resp.Health)
	}

	// Fresh heartbeat + applied, still-valid manifest + decision +
	// health snapshot (diagnostics spec §8.3, relayed verbatim).
	st := storage.EdgeSiteStatus{
		SiteID:             "ze",
		HeartbeatAt:        now.Add(-45 * time.Second),
		Status:             "shadow",
		Health:             []byte(`{"ts":"2026-08-26T11:59:30Z","ok":true,"checks":[]}`),
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
	var health struct {
		OK bool `json:"ok"`
	}
	if resp.Health == nil {
		t.Fatalf("fresh: health missing from status response")
	}
	if err := json.Unmarshal(resp.Health, &health); err != nil || !health.OK {
		t.Errorf("fresh: health = %s (err %v), want ok:true passthrough", resp.Health, err)
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
