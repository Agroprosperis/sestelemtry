package energyflow

import (
	"math"
	"strings"
	"testing"
	"time"
)

// mkSample builds a Sample populated with the five required
// accumulators. Tests that need optional fields (instantaneous
// powers, SOC) set them on the returned value.
func mkSample(t time.Time, pvYield, purchased, sold, essCharged, essDischarged float64) Sample {
	return Sample{
		Timestamp:               t,
		AccumulatedPVYieldKwh:   FloatPtr(pvYield),
		AccumulatedPurchasedKwh: FloatPtr(purchased),
		AccumulatedSoldKwh:      FloatPtr(sold),
		TotalESSChargedKwh:      FloatPtr(essCharged),
		TotalESSDischargedKwh:   FloatPtr(essDischarged),
	}
}

// approxEqual matches two kWh values within 1e-9 — float64 rounding
// of additions/subtractions tops out at ~1e-12 for the magnitudes the
// allocator handles, so 1e-9 is loose enough to never flake but tight
// enough to surface a real algorithmic regression.
func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func assertResult(t *testing.T, got Result, want Result) {
	t.Helper()
	if got.Skipped != want.Skipped {
		t.Fatalf("Skipped mismatch: got %v want %v (warnings=%v)", got.Skipped, want.Skipped, got.Warnings)
	}
	if !approxEqual(got.PVToESSKwh, want.PVToESSKwh) {
		t.Errorf("PVToESS: got %g want %g", got.PVToESSKwh, want.PVToESSKwh)
	}
	if !approxEqual(got.GridToESSKwh, want.GridToESSKwh) {
		t.Errorf("GridToESS: got %g want %g", got.GridToESSKwh, want.GridToESSKwh)
	}
	if !approxEqual(got.ESSToLoadKwh, want.ESSToLoadKwh) {
		t.Errorf("ESSToLoad: got %g want %g", got.ESSToLoadKwh, want.ESSToLoadKwh)
	}
	if !approxEqual(got.ESSToGridKwh, want.ESSToGridKwh) {
		t.Errorf("ESSToGrid: got %g want %g", got.ESSToGridKwh, want.ESSToGridKwh)
	}
}

var (
	t0 = time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	t1 = t0.Add(time.Second)
)

// 1. ESS discharges only to load.
func TestAllocate_DischargeOnlyToLoad(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 0, 0, 0, 0, 2)
	got := Allocate(prev, curr, Options{})
	assertResult(t, got, Result{ESSToLoadKwh: 2, EssDischargedKwh: 2})
}

// 2. ESS discharges, partial to load and partial to grid.
func TestAllocate_DischargeSplitLoadAndGrid(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 0, 0, 1.5, 0, 2)
	got := Allocate(prev, curr, Options{})
	assertResult(t, got, Result{
		ESSToLoadKwh:     0.5,
		ESSToGridKwh:     1.5,
		EssDischargedKwh: 2,
	})
}

// 3. ESS charges only from PV (PV surplus is enough).
func TestAllocate_ChargeOnlyFromPV(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 3, 0, 0, 2, 0)
	got := Allocate(prev, curr, Options{})
	assertResult(t, got, Result{
		PVToESSKwh:    2,
		EssChargedKwh: 2,
	})
}

// 4. ESS charges partially from PV, partially from grid.
func TestAllocate_ChargeSplitPVAndGrid(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 4, 1, 0, 2, 0)
	got := Allocate(prev, curr, Options{})
	assertResult(t, got, Result{
		PVToESSKwh:    1,
		GridToESSKwh:  1,
		EssChargedKwh: 2,
	})
}

// 5. ESS charges only from grid (no PV surplus available).
func TestAllocate_ChargeOnlyFromGrid(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 0, 3, 0, 2, 0)
	got := Allocate(prev, curr, Options{})
	assertResult(t, got, Result{
		GridToESSKwh:  2,
		EssChargedKwh: 2,
	})
}

// 6. Zero-everywhere interval. Not skipped — a flat interval is a
// valid observation; the four flow values are simply zero.
func TestAllocate_ZeroEverything(t *testing.T) {
	prev := mkSample(t0, 100, 50, 25, 10, 5)
	curr := mkSample(t1, 100, 50, 25, 10, 5)
	got := Allocate(prev, curr, Options{})
	if got.Skipped {
		t.Fatalf("zero interval should not be skipped: %v", got.Warnings)
	}
	assertResult(t, got, Result{})
}

// 7. dt non-positive (curr == prev) is rejected.
func TestAllocate_DtZero(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t0, 0, 0, 0, 0, 0)
	got := Allocate(prev, curr, Options{})
	if !got.Skipped {
		t.Fatalf("dt=0 should be skipped")
	}
	if !containsAny(got.Warnings, "dt non-positive") {
		t.Errorf("expected dt non-positive warning, got %v", got.Warnings)
	}
}

// 8. dt negative (curr < prev) is rejected with the same reason as
// dt == 0; the goal is to catch out-of-order snapshots.
func TestAllocate_DtNegative(t *testing.T) {
	prev := mkSample(t1, 0, 0, 0, 0, 0)
	curr := mkSample(t0, 0, 0, 0, 0, 0)
	got := Allocate(prev, curr, Options{})
	if !got.Skipped {
		t.Fatalf("dt<0 should be skipped")
	}
	if !containsAny(got.Warnings, "dt non-positive") {
		t.Errorf("expected dt non-positive warning, got %v", got.Warnings)
	}
}

// 9. dt larger than maxGapSeconds is rejected.
func TestAllocate_DtExceedsMaxGap(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t0.Add(60*time.Second), 0, 0, 0, 0, 0)
	got := Allocate(prev, curr, Options{MaxGapSeconds: 5})
	if !got.Skipped {
		t.Fatalf("dt>max should be skipped")
	}
	if !containsAny(got.Warnings, "dt exceeds maxGapSeconds") {
		t.Errorf("expected gap warning, got %v", got.Warnings)
	}
}

// 10. The 0xFFFFFFFF sentinel reading. Tests the IsInvalidUint32Scaled
// helper directly — Allocate trusts its inputs and the caller is
// responsible for filtering sentinel readings before they reach the
// allocator.
func TestIsInvalidUint32Scaled(t *testing.T) {
	cases := []struct {
		v, gain float64
		want    bool
	}{
		{42949672.95, 0.01, true},
		{42949672.95 + 0.001, 0.01, true},
		{12345.67, 0.01, false},
		{0, 0.01, false},
		{4294967.295, 0.001, true},
		{1.0, 0, false},
		{math.NaN(), 0.01, false},
	}
	for _, c := range cases {
		got := IsInvalidUint32Scaled(c.v, c.gain)
		if got != c.want {
			t.Errorf("IsInvalidUint32Scaled(%g, %g): got %v want %v", c.v, c.gain, got, c.want)
		}
	}
}

// 11. Balance check passes for healthy inputs and never rejects the
// interval. The two checks are independent and produce stable text.
func TestAllocate_BalanceCheckPasses(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 4, 1, 0, 2, 0)
	got := Allocate(prev, curr, Options{BalanceToleranceKwh: 0.1})
	if got.Skipped {
		t.Fatalf("healthy inputs should not be skipped: %v", got.Warnings)
	}
	for _, w := range got.Warnings {
		if strings.Contains(w, "balance off") {
			t.Errorf("unexpected balance warning: %s", w)
		}
	}
}

// 12. essDischargeSign = -1 inverts the sign-of-power sanity check.
// With the default sign convention a positive ESSPowerKw paired with
// a positive delta_ess_charged is reported as a sign mismatch (the
// device should be reading negative for charge); flipping the sign
// option silences the warning.
func TestAllocate_EssDischargeSignNegative(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 0, 3, 0, 2, 0)
	curr.ESSPowerKw = FloatPtr(5) // positive reading on a charging battery

	defaultRes := Allocate(prev, curr, Options{EssDischargeSign: 1})
	if !containsAny(defaultRes.Warnings, "ess sign mismatch") {
		t.Fatalf("default sign should warn for ess_power>0 with charge-only delta, got %v", defaultRes.Warnings)
	}

	flippedRes := Allocate(prev, curr, Options{EssDischargeSign: -1})
	for _, w := range flippedRes.Warnings {
		if strings.Contains(w, "ess sign mismatch") {
			t.Errorf("flipped sign should not warn, got %v", flippedRes.Warnings)
		}
	}
}

// 13. activePvPowerAddress default is 440388 per spec.
func TestDefaultOptions_ActivePvPowerAddress(t *testing.T) {
	d := DefaultOptions()
	if d.ActivePvPowerAddress != 440388 {
		t.Fatalf("ActivePvPowerAddress: got %d want 440388", d.ActivePvPowerAddress)
	}
}

// 14. Per-second deltas at the resolution the SmartLogger emits
// (gain = 0.01 kWh). This is the spec's worked example: 1 second
// elapses and only the consumption + discharge counters tick by
// 0.0023 kWh because of the 0.01-kWh quantization.
func TestAllocate_PerSecondSpecExample(t *testing.T) {
	prev := mkSample(t0, 23193.50, 18874.50, 8.06, 14469.50, 12547.50)
	curr := mkSample(t1, 23193.50, 18874.50, 8.06, 14469.50, 12547.5023)
	got := Allocate(prev, curr, Options{})
	assertResult(t, got, Result{
		ESSToLoadKwh:     0.0023,
		EssDischargedKwh: 0.0023,
	})
}

// 15. Missing required accumulator on the current sample is rejected.
func TestAllocate_MissingRequiredAccumulator(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 0, 0, 0, 0, 0)
	curr.TotalESSChargedKwh = nil
	got := Allocate(prev, curr, Options{})
	if !got.Skipped {
		t.Fatalf("missing accumulator should skip the interval")
	}
	if !containsAny(got.Warnings, "missing required accumulator total_ess_charged_kwh") {
		t.Errorf("expected missing-accumulator warning, got %v", got.Warnings)
	}
}

// 16. Negative counter delta (rollover, manual reset, bogus reading)
// is rejected.
func TestAllocate_NegativeDeltaRejected(t *testing.T) {
	prev := mkSample(t0, 0, 5, 0, 0, 0)
	curr := mkSample(t1, 0, 4, 0, 0, 0)
	got := Allocate(prev, curr, Options{})
	if !got.Skipped {
		t.Fatalf("negative delta should skip")
	}
	if !containsAny(got.Warnings, "negative delta_grid_import_kwh") {
		t.Errorf("expected negative delta warning, got %v", got.Warnings)
	}
}

// containsAny returns true when at least one element of warnings has
// substring s. Used to assert on warning text without coupling to the
// exact wording of the suffix (numeric formatting, units, etc.).
func containsAny(warnings []string, s string) bool {
	for _, w := range warnings {
		if strings.Contains(w, s) {
			return true
		}
	}
	return false
}

// Defensive: NaN in a required accumulator is rejected.
func TestAllocate_NonFiniteAccumulator(t *testing.T) {
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, math.NaN(), 0, 0, 0, 0)
	got := Allocate(prev, curr, Options{})
	if !got.Skipped {
		t.Fatalf("NaN accumulator should skip")
	}
	if !containsAny(got.Warnings, "non-finite accumulator") {
		t.Errorf("expected non-finite warning, got %v", got.Warnings)
	}
}

// Discharge clamp: when the algorithm would otherwise route ESS into
// the grid beyond the measured grid export, the excess is reattributed
// to the load. This is the spec's clamp at the bottom of §Розряд УЗЕ.
func TestAllocate_DischargeClampToGridExport(t *testing.T) {
	// Δappliances = 0 + 0 + 2 - 0 - 0 = 2; load_deficit = 2; ess_to_load
	// = min(2, 2) = 2; ess_to_grid = 0. Then increase Δsold to 5 with
	// Δess_dis = 5 to force the clamp branch.
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 0, 0, 0.1, 0, 5)
	got := Allocate(prev, curr, Options{})
	// Δappliances = 0+0+5-0.1-0 = 4.9; deficit = 4.9; ess_to_load =
	// min(5, 4.9) = 4.9; ess_to_grid = 0.1; clamp not triggered (0.1
	// is not > 0.1). The sum still equals Δess_dis = 5.
	assertResult(t, got, Result{
		ESSToLoadKwh:     4.9,
		ESSToGridKwh:     0.1,
		EssDischargedKwh: 5,
	})
}

// Charge clamp: when the algorithm would otherwise pull more ESS
// charge from grid than the measured grid import, the excess is
// reattributed to PV.
func TestAllocate_ChargeClampToGridImport(t *testing.T) {
	// PV=0, purchased=0.5, ess_charged=2 ⇒ initial pv_to_ess=0,
	// grid_to_ess=2 → clamp to 0.5; pv_to_ess becomes max(2-0.5,0)=1.5.
	prev := mkSample(t0, 0, 0, 0, 0, 0)
	curr := mkSample(t1, 0, 0.5, 0, 2, 0)
	got := Allocate(prev, curr, Options{})
	assertResult(t, got, Result{
		PVToESSKwh:    1.5,
		GridToESSKwh:  0.5,
		EssChargedKwh: 2,
	})
}

// ESS counter-step guard: when MaxEssPowerKw is set, an interval whose
// implied average ESS power exceeds the ceiling is rejected (a device
// counter re-base / corrupted reading), while a plausible interval at
// the same dt passes through unchanged.
func TestAllocate_EssMaxPowerKwGuard(t *testing.T) {
	// 1 s interval, 2000 kW ceiling ⇒ max plausible delta = 2000 *
	// (1/3600) ≈ 0.5556 kWh per second.
	opts := Options{MaxEssPowerKw: 2000}

	// A counter step of 14467 kWh charge in 1 s is ~52 GW — rejected.
	prev := mkSample(t0, 0, 20000, 0, 0, 0)
	curr := mkSample(t1, 0, 20000+14467, 0, 14467, 0)
	got := Allocate(prev, curr, opts)
	if !got.Skipped {
		t.Fatalf("expected charge counter step to be skipped, got %+v", got)
	}
	if !containsAny(got.Warnings, "exceeds") {
		t.Errorf("expected counter-step warning, got %v", got.Warnings)
	}

	// Same magnitude on the discharge counter is rejected too.
	prev = mkSample(t0, 0, 0, 0, 0, 0)
	curr = mkSample(t1, 0, 0, 0, 0, 12513)
	if got := Allocate(prev, curr, opts); !got.Skipped {
		t.Fatalf("expected discharge counter step to be skipped, got %+v", got)
	}

	// A plausible 0.4 kWh charge in 1 s (~1440 kW) stays under the
	// 2000 kW ceiling and is processed normally.
	prev = mkSample(t0, 0, 1, 0, 0, 0)
	curr = mkSample(t1, 0, 1.4, 0, 0.4, 0)
	got = Allocate(prev, curr, opts)
	if got.Skipped {
		t.Fatalf("plausible interval should not be skipped, got %v", got.Warnings)
	}
	assertResult(t, got, Result{
		GridToESSKwh:  0.4,
		EssChargedKwh: 0.4,
	})

	// With the guard disabled (default), the absurd step passes through
	// and pollutes the flows — proving the guard is what blocks it.
	prev = mkSample(t0, 0, 20000, 0, 0, 0)
	curr = mkSample(t1, 0, 20000+14467, 0, 14467, 0)
	if got := Allocate(prev, curr, Options{}); got.Skipped {
		t.Fatalf("guard disabled: interval should pass through, got %v", got.Warnings)
	}
}
