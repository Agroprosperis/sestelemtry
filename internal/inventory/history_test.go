package inventory_test

import (
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/inventory"
)

func fptr(v float64) *float64 { return &v }

func TestDiffHistoryDetectsChanges(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(time.Hour)
	t2 := t1.Add(time.Hour)
	snaps := []inventory.Snapshot{
		{Time: t0, PollReason: "startup", PVRatedKw: fptr(450), ESSCount: fptr(3)},
		{Time: t1, PollReason: "hourly", PVRatedKw: fptr(450), ESSCount: fptr(3)}, // no change
		{Time: t2, PollReason: "daily", PVRatedKw: fptr(600), ESSCount: fptr(8)},
	}
	diff := inventory.DiffHistory(snaps)
	pv := diff[inventory.MetricPVRatedKw]
	if len(pv) != 1 {
		t.Fatalf("pv changes: %#v", pv)
	}
	if pv[0].At != t2 || *pv[0].From != 450 || *pv[0].To != 600 {
		t.Fatalf("pv event: %#v", pv[0])
	}
	ess := diff[inventory.MetricESSCount]
	if len(ess) != 1 || *ess[0].From != 3 || *ess[0].To != 8 {
		t.Fatalf("ess_count: %#v", ess)
	}
}

func TestDiffHistoryIgnoresNoiseWithinTolerance(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	snaps := []inventory.Snapshot{
		{Time: t0, PVRatedKw: fptr(450.0)},
		{Time: t0.Add(time.Hour), PVRatedKw: fptr(450.2)}, // within 0.5
	}
	diff := inventory.DiffHistory(snaps)
	if len(diff[inventory.MetricPVRatedKw]) != 0 {
		t.Fatalf("expected no change, got %#v", diff[inventory.MetricPVRatedKw])
	}
}

func TestDiffHistoryNewestFirstAndAcceptsDescInput(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(24 * time.Hour)
	// Newest first as returned by ListPlantInventorySnapshots.
	snaps := []inventory.Snapshot{
		{Time: t1, PVRatedKw: fptr(600)},
		{Time: t0, PVRatedKw: fptr(450)},
	}
	diff := inventory.DiffHistory(snaps)
	pv := diff[inventory.MetricPVRatedKw]
	if len(pv) != 1 || pv[0].At != t1 {
		t.Fatalf("expected newest-first single event at t1: %#v", pv)
	}
}
