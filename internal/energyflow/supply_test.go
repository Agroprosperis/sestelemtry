package energyflow

import (
	"math/rand"
	"testing"
	"time"
)

var supplyT0 = time.Date(2026, 11, 15, 18, 0, 0, 0, time.UTC)

// minuteOpts lets a direct Allocate call span a one-minute window.
var minuteOpts = Options{MaxGapSeconds: 3600}

// genRow is one generator snapshot.
func genRow(t time.Time, gridBits, genBits, kw, volts float64) RawSample {
	return RawSample{Time: t, Role: RoleGen, Values: map[string]float64{
		SrcGridBreakersOnBits: gridBits,
		SrcGenBreakersOnBits:  genBits,
		SrcActiveGenPowerKw:   kw,
		SrcGenBusVoltageAV:    volts,
	}}
}

func meterRow(t time.Time, kwh, alive float64) RawSample {
	return RawSample{Time: t, Role: RoleGen, Values: map[string]float64{
		SrcAccumulatedGenEnergyKwh: kwh,
		SrcGenMeterAlive:           alive,
	}}
}

func TestSupplyModes(t *testing.T) {
	cases := []struct {
		name                       string
		gridBits, genBits, kw, bus float64
		want                       SupplyMode
	}{
		{"grid", 1, 0, 0, 230, SupplyGrid},
		{"generator by breaker", 0, 1, 0, 230, SupplyGenerator},
		{"generator by power (no breaker signal)", 0, 0, 120, 230, SupplyGenerator},
		{"island", 0, 0, 0, 228, SupplyIsland},
		{"blackout", 0, 0, 0, 0, SupplyBlackout},
		{"transition", 2, 1, 40, 230, SupplyTransition},
	}
	for _, c := range cases {
		end := supplyT0.Add(time.Minute)
		tl := BuildGenTimeline([]RawSample{genRow(end.Add(-5*time.Second), c.gridBits, c.genBits, c.kw, c.bus)},
			supplyT0, end, time.Minute, time.Time{})
		w, ok := tl.Window(end)
		if !ok || w.Mode != c.want {
			t.Errorf("%s: mode %q (ok=%v), want %q", c.name, w.Mode, ok, c.want)
		}
	}
}

// Readings older than 30 s at a window's end make it unknown; windows
// before the organization's first generator sample have no state.
func TestTimelineUnknownAndSince(t *testing.T) {
	rows := []RawSample{
		genRow(supplyT0.Add(65*time.Second), 1, 0, 0, 230),
		genRow(supplyT0.Add(175*time.Second), 1, 0, 0, 230),
	}
	since := supplyT0.Add(65 * time.Second)
	tl := BuildGenTimeline(rows, supplyT0, supplyT0.Add(4*time.Minute), time.Minute, since)
	if _, ok := tl.Window(supplyT0.Add(time.Minute)); ok {
		t.Fatal("a window ending before the first sample ever has no generator state")
	}
	want := map[time.Duration]SupplyMode{
		2 * time.Minute: SupplyUnknown, // last reading 55 s old
		3 * time.Minute: SupplyGrid,
		4 * time.Minute: SupplyUnknown,
	}
	for d, mode := range want {
		if w, _ := tl.Window(supplyT0.Add(d)); w.Mode != mode {
			t.Errorf("window ending +%s: %q, want %q", d, w.Mode, mode)
		}
	}
	h, _ := tl.Hour(supplyT0)
	if h.Minutes[SupplyUnknown] != 2 || h.Minutes[SupplyGrid] != 1 {
		t.Fatalf("minutes = %v", h.Minutes)
	}
}

// Кролевець, 2026-11-15: the meter's 36 kWh over the hour are spread in
// proportion to the power integral and add up to exactly the meter.
func TestTimelineGenEnergyFollowsMeter(t *testing.T) {
	var rows []RawSample
	rows = append(rows, meterRow(supplyT0.Add(-time.Minute), 331266, 1))
	for s := 0; s <= 3600; s += 5 {
		at := supplyT0.Add(time.Duration(s) * time.Second)
		kw := 200.0
		if s >= 1800 {
			kw = 400 // the second half-hour ran twice as hard
		}
		rows = append(rows, genRow(at, 0, 1, kw, 230))
	}
	rows = append(rows, meterRow(supplyT0.Add(30*time.Minute), 331280, 1), meterRow(supplyT0.Add(time.Hour), 331302, 1))
	tl := BuildGenTimeline(rows, supplyT0, supplyT0.Add(time.Hour), time.Minute, time.Time{})

	h, _ := tl.Hour(supplyT0)
	if h.GenKwh != 36 || h.Estimated {
		t.Fatalf("hour = %+v, want the meter's 36 kWh", h)
	}
	total := tl.GenBetween(supplyT0, supplyT0.Add(time.Hour))
	if d := total - 36; d > 1e-9 || d < -1e-9 {
		t.Fatalf("windows sum to %g kWh, want 36", total)
	}
	early, _ := tl.Window(supplyT0.Add(10 * time.Minute))
	late, _ := tl.Window(supplyT0.Add(50 * time.Minute))
	if r := late.GenKwh / early.GenKwh; r < 1.99 || r > 2.01 {
		t.Fatalf("windows %g vs %g kWh: the split must follow the power", early.GenKwh, late.GenKwh)
	}
	if h.Minutes[SupplyGenerator] != 60 {
		t.Fatalf("minutes = %v", h.Minutes)
	}
}

// A day's query loads nothing from the day before: the first hour's
// delta starts at its own first reading.
func TestTimelineMeterBaseAtDayStart(t *testing.T) {
	rows := []RawSample{meterRow(supplyT0.Add(10*time.Minute), 500, 1), meterRow(supplyT0.Add(time.Hour), 512, 1)}
	for s := 600; s <= 3600; s += 60 {
		rows = append(rows, genRow(supplyT0.Add(time.Duration(s)*time.Second), 0, 1, 24, 230))
	}
	tl := BuildGenTimeline(rows, supplyT0, supplyT0.Add(time.Hour), time.Minute, time.Time{})
	if h, _ := tl.Hour(supplyT0); h.GenKwh != 12 || h.Estimated {
		t.Fatalf("hour = %+v, want 12 kWh from the hour's first reading", h)
	}
}

// With the meter offline the hour falls back to the power integral and
// says so.
func TestTimelineEstimatesWithoutMeter(t *testing.T) {
	rows := []RawSample{meterRow(supplyT0, 100, 1), meterRow(supplyT0.Add(30*time.Minute), 100, 0)}
	for s := 0; s <= 3600; s += 60 {
		rows = append(rows, genRow(supplyT0.Add(time.Duration(s)*time.Second), 0, 1, 300, 230))
	}
	tl := BuildGenTimeline(rows, supplyT0, supplyT0.Add(time.Hour), time.Minute, time.Time{})
	h, _ := tl.Hour(supplyT0)
	if !h.Estimated || h.GenKwh < 299 || h.GenKwh > 301 {
		t.Fatalf("hour = %+v, want ~300 kWh estimated from power", h)
	}
}

// The open-day query returns rows out of time order; the timeline must
// not care.
func TestTimelineIgnoresInputOrder(t *testing.T) {
	var rows []RawSample
	for s := 0; s <= 600; s += 5 {
		rows = append(rows, genRow(supplyT0.Add(time.Duration(s)*time.Second), 0, 1, float64(100+s), 230))
	}
	rows = append(rows, meterRow(supplyT0, 10, 1), meterRow(supplyT0.Add(10*time.Minute), 40, 1))
	sorted := BuildGenTimeline(rows, supplyT0, supplyT0.Add(10*time.Minute), time.Minute, time.Time{})
	shuffled := append([]RawSample(nil), rows...)
	rand.New(rand.NewSource(1)).Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	other := BuildGenTimeline(shuffled, supplyT0, supplyT0.Add(10*time.Minute), time.Minute, time.Time{})
	for m := 1; m <= 10; m++ {
		end := supplyT0.Add(time.Duration(m) * time.Minute)
		a, _ := sorted.Window(end)
		b, _ := other.Window(end)
		if a != b {
			t.Fatalf("window +%dm: %+v vs %+v", m, a, b)
		}
	}
}

func single(t time.Time, pv, imp, exp, chg, dis float64) Sample {
	return Sample{
		Timestamp:               t,
		AccumulatedPVYieldKwh:   FloatPtr(pv),
		AccumulatedPurchasedKwh: FloatPtr(imp),
		AccumulatedSoldKwh:      FloatPtr(exp),
		TotalESSChargedKwh:      FloatPtr(chg),
		TotalESSDischargedKwh:   FloatPtr(dis),
	}
}

// Жмеринка, 2026-11-15 20:02: the generator gives 6.3 kWh in a minute,
// 2.2 of it into the battery. With the grid meter gone (its counters
// read 0.00) the interval is kept and nothing is booked on the grid.
func TestAllocateGenChargeFromGenerator(t *testing.T) {
	prev := single(supplyT0, 500, 1000, 200, 300, 400)
	curr := single(supplyT0.Add(time.Minute), 500, 0, 0, 302.2, 400)
	g := GenWindow{Mode: SupplyGenerator, GenKwh: 6.3}
	if plain := Allocate(prev, curr, minuteOpts); !plain.Skipped {
		t.Fatal("the plain allocation skips a grid meter that dropped to zero")
	}
	r := AllocateGen(prev, curr, g, minuteOpts)
	if r.Skipped {
		t.Fatalf("skipped: %v", r.Warnings)
	}
	near := func(got, want float64) bool { return got-want < 1e-9 && want-got < 1e-9 }
	if !near(r.GenToESSKwh, 2.2) || r.GridToESSKwh != 0 || !near(r.GenToLoadKwh, 4.1) || !near(r.LoadKwh, 4.1) {
		t.Fatalf("result = %+v", r)
	}
	if !near(r.LoadOffgridKwh, 4.1) || r.OffgridImportKwh != 0 {
		t.Fatalf("off-grid parts = %+v", r)
	}

	// The meter coming back before the breaker state does: the jump is
	// noted, not allocated.
	back := single(supplyT0.Add(2*time.Minute), 500, 1000.4, 200, 302.2, 400)
	r = AllocateGen(curr, back, GenWindow{Mode: SupplyGenerator}, minuteOpts)
	if r.Skipped || r.OffgridImportKwh != 1000.4 || r.GridToESSKwh != 0 {
		t.Fatalf("meter jump in an off-grid window: %+v", r)
	}
}

// Back on the grid, an unmended meter jump is no interval at all.
func TestAllocateGenRejectsGridMeterStep(t *testing.T) {
	prev := single(supplyT0, 500, 0, 0, 300, 400)
	curr := single(supplyT0.Add(time.Minute), 500, 1000.4, 200, 300, 400)
	if r := AllocateGen(prev, curr, GenWindow{Mode: SupplyGrid}, minuteOpts); !r.Skipped {
		t.Fatalf("a 1000 kWh minute must be rejected: %+v", r)
	}
}

// In grid mode without generator energy the generator-aware split is
// the plain one.
func TestAllocateGenGridMatchesAllocate(t *testing.T) {
	prev := single(supplyT0, 100, 50, 10, 20, 5)
	curr := single(supplyT0.Add(time.Minute), 101, 51.5, 10.2, 21, 5.4)
	p := Allocate(prev, curr, minuteOpts)
	g := AllocateGen(prev, curr, GenWindow{Mode: SupplyGrid}, minuteOpts)
	if g.Skipped || g.PVToESSKwh != p.PVToESSKwh || g.GridToESSKwh != p.GridToESSKwh ||
		g.ESSToLoadKwh != p.ESSToLoadKwh || g.ESSToGridKwh != p.ESSToGridKwh || g.GenToESSKwh != 0 || g.GenToLoadKwh != 0 {
		t.Fatalf("plain %+v vs generator-aware %+v", p, g)
	}
}

// A day with generator rows: the plain flows are exactly those of the
// same day without them, and the generator-aware walk sees the island,
// the blackout and the generator charging the battery.
func TestIterateWithGenKeepsPlainFlows(t *testing.T) {
	var smart, gen []RawSample
	pv, imp, chg, dis := 500.0, 1000.0, 300.0, 400.0
	for m := 0; m <= 12; m++ {
		at := supplyT0.Add(time.Duration(m) * time.Minute)
		var mode SupplyMode
		switch {
		case m < 4:
			mode = SupplyIsland
			dis += 1
		case m < 7:
			mode = SupplyBlackout
		default:
			mode = SupplyGenerator
			chg += 2
		}
		smart = append(smart, RawSample{Time: at, Role: RoleSingle, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   pv,
			SrcAccumulatedPurchasedKwh: imp,
			SrcAccumulatedSoldKwh:      200,
			SrcTotalEssChargedKwh:      chg,
			SrcTotalEssDischargedKwh:   dis,
		}})
		for s := 5; s <= 60; s += 5 {
			ts := at.Add(time.Duration(s-60) * time.Second)
			switch mode {
			case SupplyIsland:
				gen = append(gen, genRow(ts, 0, 0, 0, 228))
			case SupplyBlackout:
				gen = append(gen, genRow(ts, 0, 0, 0, 0))
			default:
				gen = append(gen, genRow(ts, 0, 1, 360, 232))
			}
		}
	}
	gen = append(gen, meterRow(supplyT0, 77700, 1), meterRow(supplyT0.Add(12*time.Minute), 77736, 1))

	collect := func(rows []RawSample, tl *GenTimeline) (plain []Result, aware []GenResult) {
		IterateIntervalsWithGen(rows, tl, Options{AllocationWindowSeconds: 60}, func(_ time.Time, r Result) {
			plain = append(plain, r)
		}, func(_ time.Time, r GenResult) {
			aware = append(aware, r)
		}, nil)
		return plain, aware
	}
	without, _ := collect(smart, nil)
	all := append(append([]RawSample(nil), smart...), gen...)
	tl := BuildGenTimeline(gen, supplyT0, supplyT0.Add(12*time.Minute), time.Minute, time.Time{})
	with, aware := collect(all, tl)
	if len(with) != len(without) {
		t.Fatalf("plain intervals %d vs %d", len(with), len(without))
	}
	for i := range with {
		a, b := with[i], without[i]
		if a.PVToESSKwh != b.PVToESSKwh || a.GridToESSKwh != b.GridToESSKwh || a.ESSToLoadKwh != b.ESSToLoadKwh || a.ESSToGridKwh != b.ESSToGridKwh {
			t.Fatalf("interval %d changed: %+v vs %+v", i, a, b)
		}
	}

	h, _ := tl.Hour(supplyT0)
	if h.Minutes[SupplyBlackout] < 2 {
		t.Fatalf("blackout minutes = %v", h.Minutes)
	}
	var genToEss, gridToEss, islandEss float64
	for _, r := range aware {
		genToEss += r.GenToESSKwh
		gridToEss += r.GridToESSKwh
		islandEss += r.ESSToLoadIslandKwh
	}
	if genToEss < 9.99 || gridToEss != 0 || islandEss < 2.99 {
		t.Fatalf("gen_to_ess %g, grid_to_ess %g, island ess_to_load %g", genToEss, gridToEss, islandEss)
	}
}
