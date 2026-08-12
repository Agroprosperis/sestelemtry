package edge

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func testBlackbox(t *testing.T) *Blackbox {
	t.Helper()
	bb, err := OpenBlackbox(BlackboxConfig{
		Enabled:         true,
		DBPath:          filepath.Join(t.TempDir(), "blackbox.db"),
		RetentionDays:   30,
		DiskCriticalPct: 95,
	}, "ab")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bb.Close() })
	return bb
}

func TestBlackboxRoundTrip(t *testing.T) {
	ctx := context.Background()
	bb := testBlackbox(t)

	tick := testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 50, "soc_percent": 40,
	}, QualityOK)
	if err := bb.WriteTick(ctx, tick); err != nil {
		t.Fatal(err)
	}

	d, _ := Decide(tick, nil, testCfg())
	if err := bb.WriteDecision(ctx, d); err != nil {
		t.Fatal(err)
	}

	if err := bb.WriteEvent(ctx, Event{
		TS: testTS, Severity: SevWarning, Code: EvSLPollFail,
		Message: "test", Context: map[string]any{"host": "mock"},
	}); err != nil {
		t.Fatal(err)
	}

	n, err := bb.PendingCount(ctx)
	if err != nil || n != 3 {
		t.Fatalf("pending = %d (%v), want 3", n, err)
	}

	ticks, err := bb.PendingTicks(ctx, 10)
	if err != nil || len(ticks.IDs) != 1 {
		t.Fatalf("ticks: %v %v", ticks, err)
	}
	var gotTick Tick
	if err := json.Unmarshal(ticks.Docs[0], &gotTick); err != nil {
		t.Fatalf("tick doc: %v", err)
	}
	if gotTick.SiteID != "ab" || gotTick.Values["active_pv_power_kw"] != 100 {
		t.Fatalf("tick doc mismatch: %+v", gotTick)
	}

	decs, err := bb.PendingDecisions(ctx, 10)
	if err != nil || len(decs.IDs) != 1 {
		t.Fatalf("decisions: %v %v", decs, err)
	}
	var rec map[string]any
	if err := json.Unmarshal(decs.Docs[0], &rec); err != nil {
		t.Fatal(err)
	}
	if rec["site_id"] != "ab" || rec["outputs"] == nil {
		t.Fatalf("decision record mismatch: %v", rec)
	}

	evs, err := bb.PendingEvents(ctx, 10)
	if err != nil || len(evs.IDs) != 1 {
		t.Fatalf("events: %v %v", evs, err)
	}
	var evDoc map[string]any
	if err := json.Unmarshal(evs.Docs[0], &evDoc); err != nil {
		t.Fatal(err)
	}
	if evDoc["code"] != EvSLPollFail {
		t.Fatalf("event doc mismatch: %v", evDoc)
	}

	// Mark everything uploaded → backlog drains.
	if err := bb.MarkUploaded(ctx, "telemetry_raw", ticks.IDs); err != nil {
		t.Fatal(err)
	}
	if err := bb.MarkUploaded(ctx, "control_decisions", decs.IDs); err != nil {
		t.Fatal(err)
	}
	if err := bb.MarkUploaded(ctx, "events", evs.IDs); err != nil {
		t.Fatal(err)
	}
	n, err = bb.PendingCount(ctx)
	if err != nil || n != 0 {
		t.Fatalf("pending after upload = %d (%v), want 0", n, err)
	}

	if err := bb.Maintain(ctx, time.Now()); err != nil {
		t.Fatalf("maintain: %v", err)
	}
}

func TestBlackboxRetentionDropsOldRows(t *testing.T) {
	ctx := context.Background()
	bb := testBlackbox(t)

	old := testTick(map[string]float64{"active_pv_power_kw": 1}, QualityOK)
	old.TS = time.Now().UTC().AddDate(0, 0, -45)
	if err := bb.WriteTick(ctx, old); err != nil {
		t.Fatal(err)
	}
	fresh := testTick(map[string]float64{"active_pv_power_kw": 2}, QualityOK)
	fresh.TS = time.Now().UTC()
	if err := bb.WriteTick(ctx, fresh); err != nil {
		t.Fatal(err)
	}

	if err := bb.Maintain(ctx, time.Now()); err != nil {
		t.Fatal(err)
	}
	ticks, err := bb.PendingTicks(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(ticks.IDs) != 1 {
		t.Fatalf("rows after retention = %d, want 1", len(ticks.IDs))
	}
}
