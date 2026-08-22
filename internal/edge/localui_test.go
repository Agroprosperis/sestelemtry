package edge

import (
	"encoding/json"
	"log/slog"
	"net/http/httptest"
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
	for _, key := range []string{"site_id", "edge_id", "mode", "uptime_s", "blackbox", "uplink"} {
		if _, ok := body[key]; !ok {
			t.Errorf("status is missing %q", key)
		}
	}
	if body["site_id"] != "ab" {
		t.Errorf("site_id = %v", body["site_id"])
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
