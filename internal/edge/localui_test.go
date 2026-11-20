package edge

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func localUITestService(t *testing.T) *Service {
	t.Helper()
	bb, err := OpenBlackbox(BlackboxConfig{
		DBPath:          t.TempDir() + "/bb.db",
		RetentionDays:   1,
		DiskCriticalPct: 95,
	}, "ab")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bb.Close() })
	cfg := &Config{SiteID: "ab", Control: ControlConfig{Mode: ModeShadow, Preset: "economic_arbitrage", Interval: time.Second}}
	cfg.Edge.EdgeID = "iot2050-ab-001"
	cfg.SmartLogger.PollInterval = time.Second
	return &Service{
		cfg:              cfg,
		log:              slog.Default(),
		bb:               bb,
		norm:             NewNormalizer(cfg),
		startedAt:        time.Now(),
		events:           make(chan Event, 8),
		eventLastWritten: map[string]time.Time{},
	}
}

func TestLocalUIStatusShape(t *testing.T) {
	s := localUITestService(t)
	rec := httptest.NewRecorder()
	s.uiStatus(rec, httptest.NewRequest("GET", "/api/status", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"site_id", "edge_id", "mode", "uptime_s", "blackbox", "uplink", "write_enabled"} {
		if _, ok := body[key]; !ok {
			t.Errorf("status is missing %q", key)
		}
	}
	if body["site_id"] != "ab" {
		t.Errorf("site_id = %v", body["site_id"])
	}
	if body["write_enabled"] != false {
		t.Errorf("write_enabled = %v, want false", body["write_enabled"])
	}
}

// §4.1: the console limits are the engine's — active manifest over the
// device YAML; YAML only before the first manifest; YAML BESS rated
// power is never presented as a limit.
func TestLocalUIStatusLimitsFollowManifest(t *testing.T) {
	s := localUITestService(t)
	s.cfg.Limits = Limits{
		Grid: GridLimits{ImportLimitKw: 516, TargetImportKw: 480},
		PV:   PVLimits{RatedKw: 660},
		Bess: BessLimits{RatedPowerKw: 324, RatedCapacityKwh: 645, SocMinEconomicPct: 20, SocMaxEconomicPct: 90},
	}
	limits := func() map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		s.uiStatus(rec, httptest.NewRequest("GET", "/api/status", nil))
		var body struct {
			Limits map[string]any `json:"limits"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.Limits
	}

	l := limits()
	if l["source"] != "config" || l["grid_import_kw"] != 516.0 || l["pv_rated_kw"] != 660.0 || l["bess_discharge_max_kw"] != 0.0 {
		t.Fatalf("pre-manifest limits = %v, want YAML fallback and no power policy", l)
	}

	now := time.Now().UTC()
	m := &Manifest{
		SiteID: "ab", Mode: ModeShadow, Preset: PresetEconomicArbitrage,
		ValidFrom: now.Add(-time.Hour), ValidUntil: now.Add(time.Hour),
		Limits:     ManifestLimits{EssChargeMaxKw: 864, EssDischargeMaxKw: 864},
		GridLimits: ManifestGridLimits{ImportLimitKw: 1700, TargetImportKw: 1600, PvRatedKw: 600},
		SocPolicy:  SocPolicy{MinEconomicPct: 25, MaxEconomicPct: 85},
	}
	s.manifest.Store(m)

	l = limits()
	want := map[string]any{
		"source": "manifest", "grid_import_kw": 1700.0, "grid_target_kw": 1600.0,
		"pv_rated_kw": 600.0, "bess_charge_max_kw": 864.0, "bess_discharge_max_kw": 864.0,
		"soc_min_pct": 25.0, "soc_max_pct": 85.0,
	}
	for k, v := range want {
		if l[k] != v {
			t.Errorf("limits[%s] = %v, want %v", k, l[k], v)
		}
	}
	for _, k := range []string{"bess_power_kw", "bess_capacity_kwh"} {
		if _, ok := l[k]; ok {
			t.Errorf("YAML %s must not be exposed as a limit", k)
		}
	}
}

func TestLocalUIOverrideLifecycle(t *testing.T) {
	s := localUITestService(t)

	// Set the safe fallback for 30 minutes.
	rec := httptest.NewRecorder()
	s.uiOverride(rec, httptest.NewRequest("POST", "/api/override",
		strings.NewReader(`{"mode":"fallback_safe","ttl_minutes":30}`)))
	if rec.Code != 200 {
		t.Fatalf("set: status = %d body=%s", rec.Code, rec.Body.String())
	}
	ov := s.override.Load()
	if !ov.activeAt(time.Now()) || ov.Mode != "fallback_safe" {
		t.Fatalf("override not active: %+v", ov)
	}

	// The tick must decide with the safe preset while overridden.
	s.onTick(t.Context(), time.Now().UTC())
	if d := s.lastDecision.Load(); d == nil {
		t.Fatal("no decision under fallback_safe override")
	} else {
		if d.Preset != FallbackPreset {
			t.Errorf("preset = %q, want %q", d.Preset, FallbackPreset)
		}
		if d.PlanSource != "override" {
			t.Errorf("plan_source = %q, want override", d.PlanSource)
		}
	}

	// Monitor mode suspends decisions entirely.
	rec = httptest.NewRecorder()
	s.uiOverride(rec, httptest.NewRequest("POST", "/api/override",
		strings.NewReader(`{"mode":"monitor","ttl_minutes":30}`)))
	if rec.Code != 200 {
		t.Fatalf("monitor: status = %d", rec.Code)
	}
	before := s.lastDecision.Load()
	s.onTick(t.Context(), time.Now().UTC())
	if after := s.lastDecision.Load(); after != before {
		t.Error("monitor override still produced a decision")
	}

	// Reset back to the manifest.
	rec = httptest.NewRecorder()
	s.uiOverride(rec, httptest.NewRequest("POST", "/api/override", strings.NewReader(`{"mode":"auto"}`)))
	if rec.Code != 200 {
		t.Fatalf("clear: status = %d", rec.Code)
	}
	if s.override.Load() != nil {
		t.Error("override not cleared")
	}

	// Bad mode → 400.
	rec = httptest.NewRecorder()
	s.uiOverride(rec, httptest.NewRequest("POST", "/api/override", strings.NewReader(`{"mode":"bogus"}`)))
	if rec.Code != 400 {
		t.Errorf("bogus mode: status = %d, want 400", rec.Code)
	}
}

func TestLocalUIRequiresLogin(t *testing.T) {
	s := localUITestService(t)
	path := filepath.Join(t.TempDir(), "console_password.json")
	s.console = &consoleGate{path: path}
	h := s.localUIHandler()

	serve := func(method, target, ip, user, pass string, csrf bool) *httptest.ResponseRecorder {
		var body *strings.Reader
		if method == http.MethodPost {
			body = strings.NewReader(`{"mode":"auto"}`)
		} else {
			body = strings.NewReader("")
		}
		req := httptest.NewRequest(method, target, body)
		req.RemoteAddr = ip + ":51000"
		if user != "" || pass != "" {
			req.SetBasicAuth(user, pass)
		}
		if csrf {
			req.Header.Set(consoleCSRFHeader, "fetch")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := serve(http.MethodGet, "/api/status", "10.0.0.5", "operator", "console pass 1", false); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no password file: status = %d, want 503", rec.Code)
	}
	if err := SetConsolePassword(path, "operator", "console pass 1", testTS); err != nil {
		t.Fatal(err)
	}

	rec := serve(http.MethodGet, "/", "10.0.0.5", "", "", false)
	if rec.Code != http.StatusUnauthorized || !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), `Basic realm="EMS edge ab"`) {
		t.Fatalf("no credentials: status = %d, WWW-Authenticate = %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if rec := serve(http.MethodGet, "/api/status", "10.0.0.5", "operator", "console pass 1", false); rec.Code != http.StatusOK {
		t.Fatalf("right login: status = %d (%s)", rec.Code, rec.Body.String())
	}
	if rec := serve(http.MethodGet, "/api/status", "10.0.0.5", "admin", "console pass 1", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong user: status = %d, want 401", rec.Code)
	}

	// The emergency override is a POST: it needs the CSRF header too.
	if rec := serve(http.MethodPost, "/api/override", "10.0.0.5", "operator", "console pass 1", false); rec.Code != http.StatusForbidden {
		t.Fatalf("POST without %s: status = %d, want 403", consoleCSRFHeader, rec.Code)
	}
	if rec := serve(http.MethodPost, "/api/override", "10.0.0.5", "operator", "console pass 1", true); rec.Code != http.StatusOK {
		t.Fatalf("POST with %s: status = %d (%s)", consoleCSRFHeader, rec.Code, rec.Body.String())
	}
	if ev := <-s.events; ev.Code != EvOverrideCleared {
		t.Fatalf("override event = %+v", ev)
	}

	// Lockout is per address: 10.0.0.66 guessing doesn't lock 10.0.0.5 out.
	for i := 1; i < consoleMaxFails; i++ {
		if rec := serve(http.MethodGet, "/api/status", "10.0.0.66", "operator", "guess guess", false); rec.Code != http.StatusUnauthorized {
			t.Fatalf("wrong password %d: status = %d, want 401", i, rec.Code)
		}
	}
	rec = serve(http.MethodGet, "/api/status", "10.0.0.66", "operator", "guess guess", false)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("fifth wrong password: status = %d, Retry-After = %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	select {
	case ev := <-s.events:
		if ev.Code != EvConsoleAuthLocked || strings.Contains(ev.Message, "guess") {
			t.Fatalf("event = %+v", ev)
		}
	default:
		t.Fatal("no CONSOLE_LOGIN_LOCKED event")
	}
	if rec := serve(http.MethodGet, "/api/status", "10.0.0.66", "operator", "console pass 1", false); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("right password during the lockout: status = %d, want 429", rec.Code)
	}
	if rec := serve(http.MethodGet, "/api/status", "10.0.0.5", "operator", "console pass 1", false); rec.Code != http.StatusOK {
		t.Fatalf("other address during the lockout: status = %d, want 200", rec.Code)
	}

	// A new password replaces the remembered one at once.
	if err := SetConsolePassword(path, "operator", "console pass 2", testTS); err != nil {
		t.Fatal(err)
	}
	if rec := serve(http.MethodGet, "/api/status", "10.0.0.5", "operator", "console pass 1", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("old password after the change: status = %d, want 401", rec.Code)
	}
	if rec := serve(http.MethodGet, "/api/status", "10.0.0.5", "operator", "console pass 2", false); rec.Code != http.StatusOK {
		t.Fatalf("new password: status = %d, want 200", rec.Code)
	}
}

func TestLocalUIOverrideExpires(t *testing.T) {
	s := localUITestService(t)
	s.override.Store(&overrideState{Mode: "monitor", Until: time.Now().Add(-time.Minute)})
	s.onTick(t.Context(), time.Now().UTC())
	if s.override.Load() != nil {
		t.Error("expired override was not cleared on tick")
	}
	// After expiry decisions flow again.
	if d := s.lastDecision.Load(); d == nil {
		t.Error("no decision after the override expired")
	}
}
