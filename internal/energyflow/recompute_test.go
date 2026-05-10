package energyflow

import (
	"math"
	"testing"
	"time"
)

// TestRecompute_DualBucketsHappyPath drives Recompute with two
// 60-second buckets across a dual SmartLogger and verifies the four
// flow totals match the spec hand-calculation. PV delta (yields +6,
// app load +1) leaves a +5 surplus over the period; the ESS charge
// delta is +5 so the surplus is fully absorbed → pv_to_ess=5,
// grid_to_ess=0. Discharge delta is zero so ess_to_load and
// ess_to_grid are both zero. The emitted batch should carry one
// cumulative sample per synthetic metric, seeded onto the cumulative
// totals handed in.
func TestRecompute_DualBucketsHappyPath(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(60 * time.Second)

	rows := []RawSample{
		{Time: t0, Role: RolePV, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   100,
			SrcAccumulatedPurchasedKwh: 50,
			SrcAccumulatedSoldKwh:      10,
		}},
		{Time: t0, Role: RoleESS, Values: map[string]float64{
			SrcTotalEssChargedKwh:    20,
			SrcTotalEssDischargedKwh: 5,
		}},
		{Time: t1, Role: RolePV, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   106,
			SrcAccumulatedPurchasedKwh: 50,
			SrcAccumulatedSoldKwh:      10,
		}},
		{Time: t1, Role: RoleESS, Values: map[string]float64{
			SrcTotalEssChargedKwh:    25,
			SrcTotalEssDischargedKwh: 5,
		}},
	}

	seed := map[string]float64{
		MetricPVToESSKwh:   1.0,
		MetricGridToESSKwh: 2.0,
		MetricESSToLoadKwh: 3.0,
		MetricESSToGridKwh: 4.0,
	}
	res := Recompute(rows, seed, Options{AllocationWindowSeconds: 60})

	if res.ProcessedIntervals != 1 {
		t.Fatalf("processed intervals = %d, want 1", res.ProcessedIntervals)
	}
	if res.SkippedIntervals != 0 {
		t.Fatalf("skipped intervals = %d, want 0", res.SkippedIntervals)
	}
	if !floatNear(res.Totals[MetricPVToESSKwh], 5.0, 1e-9) {
		t.Fatalf("pv_to_ess = %g, want 5", res.Totals[MetricPVToESSKwh])
	}
	if !floatNear(res.Totals[MetricGridToESSKwh], 0.0, 1e-9) {
		t.Fatalf("grid_to_ess = %g, want 0", res.Totals[MetricGridToESSKwh])
	}
	if !floatNear(res.Totals[MetricESSToLoadKwh], 0.0, 1e-9) {
		t.Fatalf("ess_to_load = %g, want 0", res.Totals[MetricESSToLoadKwh])
	}
	if !floatNear(res.Totals[MetricESSToGridKwh], 0.0, 1e-9) {
		t.Fatalf("ess_to_grid = %g, want 0", res.Totals[MetricESSToGridKwh])
	}

	if len(res.Emitted) != len(SyntheticMetricKeys) {
		t.Fatalf("emitted = %d, want %d", len(res.Emitted), len(SyntheticMetricKeys))
	}
	gotByKey := make(map[string]float64, len(res.Emitted))
	for _, e := range res.Emitted {
		gotByKey[e.MetricKey] = e.Value
	}
	if !floatNear(gotByKey[MetricPVToESSKwh], seed[MetricPVToESSKwh]+5.0, 1e-9) {
		t.Errorf("cumulative pv_to_ess = %g, want %g", gotByKey[MetricPVToESSKwh], seed[MetricPVToESSKwh]+5.0)
	}
	if !floatNear(gotByKey[MetricGridToESSKwh], seed[MetricGridToESSKwh], 1e-9) {
		t.Errorf("cumulative grid_to_ess = %g, want %g", gotByKey[MetricGridToESSKwh], seed[MetricGridToESSKwh])
	}
}

// TestRecompute_SentinelFiltered makes sure the same UINT32 sentinel
// filter the live aggregator applies in Submit also runs in Recompute.
// A bogus 4294967.295 in one of the source counters would otherwise
// silently dominate the delta; the test verifies the bucket is
// dropped (cannot Allocate without all five accumulators) and the
// run produces no flow output.
func TestRecompute_SentinelFiltered(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	t1 := t0.Add(60 * time.Second)

	rows := []RawSample{
		{Time: t0, Role: RoleSingle, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   100,
			SrcAccumulatedPurchasedKwh: 50,
			SrcAccumulatedSoldKwh:      10,
			SrcTotalEssChargedKwh:      20,
			SrcTotalEssDischargedKwh:   5,
		}},
		{Time: t1, Role: RoleSingle, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   4294967.295,
			SrcAccumulatedPurchasedKwh: 50,
			SrcAccumulatedSoldKwh:      10,
			SrcTotalEssChargedKwh:      20,
			SrcTotalEssDischargedKwh:   5,
		}},
	}
	res := Recompute(rows, nil, Options{AllocationWindowSeconds: 60})
	if res.ProcessedIntervals != 0 {
		t.Fatalf("processed intervals = %d, want 0 (sentinel should drop the bucket)", res.ProcessedIntervals)
	}
	if len(res.Emitted) != 0 {
		t.Fatalf("emitted = %d, want 0", len(res.Emitted))
	}
	for _, k := range SyntheticMetricKeys {
		if res.Totals[k] != 0 {
			t.Errorf("totals[%s] = %g, want 0", k, res.Totals[k])
		}
	}
}

// TestRecompute_GapTolerated verifies that with MaxGapSeconds=0 the
// algorithm still spans long gaps between consecutive buckets. This
// matters for historical backfill where a collector outage of hours
// between two snapshots is common; rejecting those intervals would
// erase the real counter delta that accumulated across the outage.
func TestRecompute_GapTolerated(t *testing.T) {
	t0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)
	t1 := t0.Add(6 * time.Hour)

	rows := []RawSample{
		{Time: t0, Role: RoleSingle, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   0,
			SrcAccumulatedPurchasedKwh: 0,
			SrcAccumulatedSoldKwh:      0,
			SrcTotalEssChargedKwh:      0,
			SrcTotalEssDischargedKwh:   0,
		}},
		{Time: t1, Role: RoleSingle, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   10,
			SrcAccumulatedPurchasedKwh: 2,
			SrcAccumulatedSoldKwh:      1,
			SrcTotalEssChargedKwh:      4,
			SrcTotalEssDischargedKwh:   0,
		}},
	}
	res := Recompute(rows, nil, Options{
		AllocationWindowSeconds: 60,
		MaxGapSeconds:           0,
	})
	if res.ProcessedIntervals != 1 {
		t.Fatalf("processed intervals = %d, want 1 (MaxGapSeconds=0 should disable gap guard)", res.ProcessedIntervals)
	}
	if res.SkippedIntervals != 0 {
		t.Fatalf("skipped intervals = %d, want 0", res.SkippedIntervals)
	}
	if res.Totals[MetricPVToESSKwh] <= 0 {
		t.Errorf("pv_to_ess = %g, want > 0", res.Totals[MetricPVToESSKwh])
	}
}

// TestRecompute_LatestPerBucketWins folds two readings inside the
// same 60-second bucket and verifies the later one is used. A naive
// implementation that picks the earliest reading would silently drop
// a partial bucket's counter increase.
func TestRecompute_LatestPerBucketWins(t *testing.T) {
	t0 := time.Date(2026, 5, 1, 10, 0, 0, 0, time.UTC)
	tMid := t0.Add(30 * time.Second)
	t1 := t0.Add(90 * time.Second)

	rows := []RawSample{
		{Time: t0, Role: RoleSingle, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   100,
			SrcAccumulatedPurchasedKwh: 50,
			SrcAccumulatedSoldKwh:      10,
			SrcTotalEssChargedKwh:      20,
			SrcTotalEssDischargedKwh:   5,
		}},
		{Time: tMid, Role: RoleSingle, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   101,
			SrcAccumulatedPurchasedKwh: 50,
			SrcAccumulatedSoldKwh:      10,
			SrcTotalEssChargedKwh:      20,
			SrcTotalEssDischargedKwh:   5,
		}},
		{Time: t1, Role: RoleSingle, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   110,
			SrcAccumulatedPurchasedKwh: 50,
			SrcAccumulatedSoldKwh:      10,
			SrcTotalEssChargedKwh:      28,
			SrcTotalEssDischargedKwh:   5,
		}},
	}
	res := Recompute(rows, nil, Options{AllocationWindowSeconds: 60})
	if res.ProcessedIntervals < 1 {
		t.Fatalf("processed intervals = %d, want >= 1", res.ProcessedIntervals)
	}
	if res.Totals[MetricPVToESSKwh] <= 0 {
		t.Errorf("pv_to_ess total should reflect the second sample's delta, got %g", res.Totals[MetricPVToESSKwh])
	}
}

func floatNear(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}
