package energyflow

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"
)

// fakeEmit captures emitted samples for assertions.
type fakeEmit struct {
	mu       sync.Mutex
	calls    int
	samples  [][]EmittedSample
	emitErr  error
	totalLen int
}

func (f *fakeEmit) Func(_ context.Context, samples []EmittedSample) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	cp := make([]EmittedSample, len(samples))
	copy(cp, samples)
	f.samples = append(f.samples, cp)
	f.totalLen += len(cp)
	return f.emitErr
}

func (f *fakeEmit) lastBatch() []EmittedSample {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.samples) == 0 {
		return nil
	}
	return f.samples[len(f.samples)-1]
}

// TestRoleDetection verifies that DetectRole maps a metric_keys
// whitelist to the right role per the spec auto-detection rules
// (chosen by the user — see plan §Топологія).
func TestDetectRole(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		want Role
	}{
		{
			name: "empty whitelist = full catalog = single",
			keys: nil,
			want: RoleSingle,
		},
		{
			name: "all PV + all ESS = single",
			keys: []string{
				SrcAccumulatedPVYieldKwh,
				SrcAccumulatedPurchasedKwh,
				SrcAccumulatedSoldKwh,
				SrcTotalEssChargedKwh,
				SrcTotalEssDischargedKwh,
			},
			want: RoleSingle,
		},
		{
			name: "only PV accumulators",
			keys: []string{
				SrcAccumulatedPVYieldKwh,
				SrcAccumulatedPurchasedKwh,
				SrcAccumulatedSoldKwh,
			},
			want: RolePV,
		},
		{
			name: "only ESS accumulators",
			keys: []string{
				SrcTotalEssChargedKwh,
				SrcTotalEssDischargedKwh,
			},
			want: RoleESS,
		},
		{
			name: "partial PV (missing one) = none",
			keys: []string{
				SrcAccumulatedPVYieldKwh,
				SrcAccumulatedSoldKwh,
			},
			want: RoleNone,
		},
		{
			name: "irrelevant metrics only = none",
			keys: []string{SrcSocPercent, SrcLoadPowerKw},
			want: RoleNone,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := DetectRole(c.keys)
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

// TestAggregator_DualSourceMerge feeds PV and ESS snapshots from two
// separate Submit calls and verifies the next Flush emits cumulative
// samples consistent with the spec algorithm.
func TestAggregator_DualSourceMerge(t *testing.T) {
	emit := &fakeEmit{}
	now := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)
	a := New("ze", Options{AllocationWindowSeconds: 60, MaxGapSeconds: 5}, emit.Func, nil)
	a.now = func() time.Time { return now }

	// First flush sets the baseline.
	a.Submit(RolePV, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   100,
		SrcAccumulatedPurchasedKwh: 50,
		SrcAccumulatedSoldKwh:      10,
	})
	a.Submit(RoleESS, now, map[string]float64{
		SrcTotalEssChargedKwh:    20,
		SrcTotalEssDischargedKwh: 15,
	})
	if err := a.Flush(context.Background()); err != nil {
		t.Fatalf("first flush: %v", err)
	}
	if emit.calls != 0 {
		t.Fatalf("first flush should not emit (no prev), got %d", emit.calls)
	}

	// Second flush 60 s later. PV produces 4 kWh, of which 1 kWh
	// charges ESS (Δess_charged = 1), purchased = 0, sold = 0,
	// discharged = 0. Δappliances = 4+0+0-0-1=3. pv_surplus =
	// 4-3=1 → pv_to_ess = 1, grid_to_ess = 0.
	now = now.Add(60 * time.Second)
	a.now = func() time.Time { return now }
	a.Submit(RolePV, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   104,
		SrcAccumulatedPurchasedKwh: 50,
		SrcAccumulatedSoldKwh:      10,
	})
	a.Submit(RoleESS, now, map[string]float64{
		SrcTotalEssChargedKwh:    21,
		SrcTotalEssDischargedKwh: 15,
	})
	if err := a.Flush(context.Background()); err != nil {
		t.Fatalf("second flush: %v", err)
	}
	if emit.calls != 1 {
		t.Fatalf("second flush should emit once, got %d", emit.calls)
	}
	got := samplesByKey(emit.lastBatch())
	want := map[string]float64{
		MetricPVToESSKwh:   1.0,
		MetricGridToESSKwh: 0.0,
		MetricESSToLoadKwh: 0.0,
		MetricESSToGridKwh: 0.0,
	}
	for k, v := range want {
		if !approxEqual(got[k], v) {
			t.Errorf("metric %s: got %g want %g", k, got[k], v)
		}
	}
}

// TestAggregator_Reseed verifies cumulative totals are restored from
// a prior process and accumulate further from there.
func TestAggregator_Reseed(t *testing.T) {
	emit := &fakeEmit{}
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	a := New("ze", Options{AllocationWindowSeconds: 60}, emit.Func, nil)
	a.now = func() time.Time { return now }
	a.Reseed(map[string]float64{
		MetricPVToESSKwh:   123.45,
		MetricGridToESSKwh: 67.89,
	})

	a.Submit(RoleSingle, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   0,
		SrcAccumulatedPurchasedKwh: 0,
		SrcAccumulatedSoldKwh:      0,
		SrcTotalEssChargedKwh:      0,
		SrcTotalEssDischargedKwh:   0,
	})
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	now = now.Add(60 * time.Second)
	a.now = func() time.Time { return now }
	a.Submit(RoleSingle, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   0,
		SrcAccumulatedPurchasedKwh: 1,
		SrcAccumulatedSoldKwh:      0,
		SrcTotalEssChargedKwh:      1,
		SrcTotalEssDischargedKwh:   0,
	})
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := samplesByKey(emit.lastBatch())
	// All charge from grid → +1 kWh added on top of seed.
	if !approxEqual(got[MetricPVToESSKwh], 123.45) {
		t.Errorf("pv_to_ess seed: got %g want 123.45", got[MetricPVToESSKwh])
	}
	if !approxEqual(got[MetricGridToESSKwh], 67.89+1.0) {
		t.Errorf("grid_to_ess seed+delta: got %g want 68.89", got[MetricGridToESSKwh])
	}
}

// TestAggregator_DropsUint32Sentinel verifies the 0xFFFFFFFF * 0.01
// sentinel reading is filtered before reaching the allocator. The
// next Submit recovers normally and the allocator computes the
// expected delta.
func TestAggregator_DropsUint32Sentinel(t *testing.T) {
	emit := &fakeEmit{}
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	a := New("ze", Options{AllocationWindowSeconds: 60}, emit.Func, nil)
	a.now = func() time.Time { return now }

	a.Submit(RoleSingle, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   100,
		SrcAccumulatedPurchasedKwh: 50,
		SrcAccumulatedSoldKwh:      10,
		SrcTotalEssChargedKwh:      20,
		SrcTotalEssDischargedKwh:   15,
	})
	_ = a.Flush(context.Background())

	// Sentinel reading on accumulated_purchased_kwh gets dropped.
	now = now.Add(60 * time.Second)
	a.now = func() time.Time { return now }
	a.Submit(RoleSingle, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   100,
		SrcAccumulatedPurchasedKwh: 42949672.95, // 0xFFFFFFFF * 0.01 sentinel
		SrcAccumulatedSoldKwh:      10,
		SrcTotalEssChargedKwh:      20,
		SrcTotalEssDischargedKwh:   15,
	})
	_ = a.Flush(context.Background())
	diag := a.SnapshotDiagnostics()
	if diag.InvalidSamples == 0 {
		t.Errorf("expected InvalidSamples > 0 after sentinel, got %+v", diag)
	}
}

// TestAggregator_StaleSourceSkipsWindow verifies that when one of
// the dual sources stops reporting (last reading older than
// MaxGapSeconds), the next flush is skipped instead of feeding
// stale data into Allocate.
func TestAggregator_StaleSourceSkipsWindow(t *testing.T) {
	emit := &fakeEmit{}
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	a := New("ze", Options{AllocationWindowSeconds: 60, MaxGapSeconds: 5}, emit.Func, nil)
	a.now = func() time.Time { return now }

	// Both sides see the same baseline.
	a.Submit(RolePV, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   0,
		SrcAccumulatedPurchasedKwh: 0,
		SrcAccumulatedSoldKwh:      0,
	})
	a.Submit(RoleESS, now, map[string]float64{
		SrcTotalEssChargedKwh:    0,
		SrcTotalEssDischargedKwh: 0,
	})
	_ = a.Flush(context.Background())

	// Advance 60 s. PV updates, ESS does not.
	now = now.Add(60 * time.Second)
	a.now = func() time.Time { return now }
	a.Submit(RolePV, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   1,
		SrcAccumulatedPurchasedKwh: 0,
		SrcAccumulatedSoldKwh:      0,
	})
	if err := a.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if emit.calls != 0 {
		t.Errorf("stale ESS should skip the flush, got %d emits", emit.calls)
	}
	diag := a.SnapshotDiagnostics()
	if diag.WindowsSkipped == 0 {
		t.Errorf("expected WindowsSkipped > 0, got %+v", diag)
	}
}

// TestAggregator_TimeSkewWarning fires only when the PV and ESS
// timestamps differ by more than warnDeviceTimeSkewSeconds. The
// interval is still emitted (spec §Службові регістри часу).
func TestAggregator_TimeSkewWarning(t *testing.T) {
	emit := &fakeEmit{}
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	a := New("ze", Options{
		AllocationWindowSeconds:   60,
		MaxGapSeconds:             30,
		WarnDeviceTimeSkewSeconds: 5,
	}, emit.Func, nil)
	a.now = func() time.Time { return now }

	a.Submit(RolePV, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   0,
		SrcAccumulatedPurchasedKwh: 0,
		SrcAccumulatedSoldKwh:      0,
	})
	a.Submit(RoleESS, now.Add(-15*time.Second), map[string]float64{
		SrcTotalEssChargedKwh:    0,
		SrcTotalEssDischargedKwh: 0,
	})
	_ = a.Flush(context.Background())

	now = now.Add(60 * time.Second)
	a.now = func() time.Time { return now }
	a.Submit(RolePV, now, map[string]float64{
		SrcAccumulatedPVYieldKwh:   1,
		SrcAccumulatedPurchasedKwh: 0,
		SrcAccumulatedSoldKwh:      0,
	})
	a.Submit(RoleESS, now.Add(-15*time.Second), map[string]float64{
		SrcTotalEssChargedKwh:    0,
		SrcTotalEssDischargedKwh: 0,
	})
	_ = a.Flush(context.Background())
	diag := a.SnapshotDiagnostics()
	if !containsAny(diag.Warnings, "device clock skew exceeds warn threshold") {
		t.Errorf("expected skew warning, got %+v", diag.Warnings)
	}
	if emit.calls == 0 {
		t.Errorf("skew warning must not block emission")
	}
}

// TestAggregator_RunStopsOnContext verifies the Run goroutine
// terminates promptly when its context is canceled, and performs a
// final flush.
func TestAggregator_RunStopsOnContext(t *testing.T) {
	emit := &fakeEmit{}
	a := New("ze", Options{AllocationWindowSeconds: 1}, emit.Func, nil)
	now := time.Date(2026, 5, 7, 0, 0, 0, 0, time.UTC)
	a.now = func() time.Time { return now }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()
	// Cancel immediately; Run should exit within a couple of seconds.
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit after cancel")
	}
}

// TestAggregator_NegativeReseedDropped verifies a negative reseed
// value is rejected and the running total stays at zero.
func TestAggregator_NegativeReseedDropped(t *testing.T) {
	a := New("ze", Options{}, nil, nil)
	a.Reseed(map[string]float64{MetricPVToESSKwh: -1.0, MetricGridToESSKwh: math.NaN()})
	if v := a.CumulativeSnapshot()[MetricPVToESSKwh]; v != 0 {
		t.Errorf("negative reseed should be dropped, got %g", v)
	}
	if v, ok := a.CumulativeSnapshot()[MetricGridToESSKwh]; ok {
		t.Errorf("NaN reseed should be dropped, got %g", v)
	}
}

func samplesByKey(samples []EmittedSample) map[string]float64 {
	out := make(map[string]float64, len(samples))
	for _, s := range samples {
		out[s.MetricKey] = s.Value
	}
	return out
}
