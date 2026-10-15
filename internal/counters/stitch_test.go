package counters

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 14, 6, 0, 0, 0, time.UTC)

func at(d time.Duration) time.Time { return t0.Add(d) }

func step(prevAt time.Duration, prev float64, curAt time.Duration, cur float64) Step {
	return Step{PrevTime: at(prevAt), Prev: prev, Time: at(curAt), Value: cur}
}

func near(t *testing.T, name string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("%s = %.6f, want %.6f", name, got, want)
	}
}

// corrected replays raw readings against the segments a run produced.
func corrected(segs []Segment, when time.Time, raw float64) float64 {
	return raw + OffsetAt(segs, when)
}

// The Дубовязівський grid meter: the import register falls to a stale
// 8300.41 kWh for 37 hours and comes back 16652.12 kWh higher. Only
// what the meter counted while it was away (2.53 kWh) is new energy,
// and it spreads across the gap instead of landing on one bucket.
func TestDropoutReturnCreditsGapAcrossIt(t *testing.T) {
	steps := []Step{
		step(0, 24950.00, time.Second, 8300.41),
		step(37*time.Hour, 8300.41, 37*time.Hour+time.Second, 24952.53),
	}
	res := Run(State{}, steps)

	if len(res.Segments) != 2 {
		t.Fatalf("segments = %+v", res.Segments)
	}
	gap := res.Segments[0]
	near(t, "gap start offset", gap.StartOffset, 16649.59)
	near(t, "gap end offset", gap.EndOffset, 16652.12)
	near(t, "offset after return", res.Segments[1].StartOffset, 0)
	if !res.Segments[1].End.IsZero() {
		t.Fatalf("last segment should stay open: %+v", res.Segments[1])
	}

	before := corrected(res.Segments, at(0), 24950.00)
	inGap := corrected(res.Segments, at(time.Second), 8300.41)
	justBack := corrected(res.Segments, at(37*time.Hour+time.Second), 24952.53)
	near(t, "fall bridged", inGap-before, 0)
	near(t, "energy across the gap", justBack-before, 2.53)

	if len(res.Episodes) != 1 {
		t.Fatalf("episodes = %+v", res.Episodes)
	}
	ep := res.Episodes[0]
	if ep.Kind != KindDropout || ep.End.IsZero() {
		t.Fatalf("episode = %+v", ep)
	}
	near(t, "gap kwh", ep.GapKwh, 2.53)
	near(t, "returned", ep.Returned, 24952.53)
	if res.State.Offset() != 0 || len(res.State.Falls) != 0 {
		t.Fatalf("state should be whole again: %+v", res.State)
	}
}

// Repeated flip-flops leave the corrected counter on the real track,
// with no phantom energy from any of the returns.
func TestRepeatedFlipFlops(t *testing.T) {
	stale := 2357.97
	var steps []Step
	real := 4195.03
	clock := time.Duration(0)
	for i := 0; i < 10; i++ {
		steps = append(steps, step(clock, real, clock+time.Second, stale))
		clock += 6 * time.Hour
		real += 1.5 // exported while the meter was away
		steps = append(steps, step(clock, stale, clock+time.Second, real))
		clock += 6 * time.Hour
		real += 3 // exported while visible; ordinary steps, not handed over
	}
	res := Run(State{}, steps)
	near(t, "final offset", res.State.Offset(), 0)
	var credited float64
	for _, e := range res.Episodes {
		if e.Kind != KindDropout || e.End.IsZero() {
			t.Fatalf("unexpected episode %+v", e)
		}
		credited += e.GapKwh
	}
	near(t, "credited", credited, 15)
}

// A lone 0 from the FusionSolar archive (the Агродар Бар case) is a
// fall to zero and a return: the recovery adds only the day's growth.
func TestZeroDropout(t *testing.T) {
	steps := []Step{
		step(0, 193120, 5*time.Minute, 0),
		step(26*time.Hour, 0, 26*time.Hour+5*time.Minute, 193409),
	}
	res := Run(State{}, steps)
	near(t, "gap", res.Episodes[0].GapKwh, 289)
	near(t, "offset", res.State.Offset(), 0)
}

// A corrupted high reading between normal neighbours is a seam that the
// matching fall turns into a spike: the garbage disappears, the real
// kWh metered across it stays.
func TestSpikeIsRemoved(t *testing.T) {
	steps := []Step{
		step(0, 100, time.Second, 28846),
		step(time.Second, 28846, 2*time.Second, 101),
	}
	res := Run(State{}, steps)
	if len(res.Episodes) != 1 || res.Episodes[0].Kind != KindSpike {
		t.Fatalf("episodes = %+v", res.Episodes)
	}
	near(t, "on the spike", corrected(res.Segments, at(time.Second), 28846), 100)
	near(t, "after the spike", corrected(res.Segments, at(2*time.Second), 101), 101)
	if len(res.State.Falls) != 0 || res.State.Jump != nil || res.State.Offset() != 0 {
		t.Fatalf("state = %+v", res.State)
	}
}

// A reading that goes stale above the real counter for three hours and
// then comes down is a spike, not a dropout that never closes; what the
// counter metered meanwhile is spread across it.
func TestStaleHighReading(t *testing.T) {
	steps := []Step{
		step(0, 5000, time.Second, 7000),
		step(3*time.Hour, 7000, 3*time.Hour+time.Second, 5300),
	}
	res := Run(State{}, steps)
	if len(res.Episodes) != 1 || res.Episodes[0].Kind != KindSpike {
		t.Fatalf("episodes = %+v", res.Episodes)
	}
	near(t, "credited", res.Episodes[0].GapKwh, 300)
	near(t, "midway", corrected(res.Segments, at(time.Second+90*time.Minute), 7000), 5150)
	near(t, "after", corrected(res.Segments, at(3*time.Hour+time.Second), 5300), 5300)
	near(t, "offset", res.State.Offset(), 0)
}

// Archive backfill handing over to live Modbus jumps to a different
// lifetime baseline (Жмеринка, 27.06.2025). The step is removed for
// good and nothing is left waiting for it.
func TestSeamIsRemoved(t *testing.T) {
	steps := []Step{step(0, 368018, 5*time.Minute, 394908)}
	res := Run(State{}, steps)
	if res.Episodes[0].Kind != KindSeam {
		t.Fatalf("episodes = %+v", res.Episodes)
	}
	near(t, "corrected", corrected(res.Segments, at(5*time.Minute), 394908), 368018)
	near(t, "base", res.State.Base, -26890)
}

// An inverter that leaves the PV sum for good keeps the dropout open:
// the counter continues from the old peak, counting what the remaining
// inverters produce.
func TestPermanentFallStaysOpen(t *testing.T) {
	steps := []Step{step(0, 184473, time.Second, 100059)}
	res := Run(State{}, steps)
	if !res.Episodes[0].End.IsZero() {
		t.Fatalf("dropout should be open: %+v", res.Episodes[0])
	}
	near(t, "next day", corrected(res.Segments, at(24*time.Hour), 100332), 184746)
	if res.State.OpenSince() != at(time.Second) {
		t.Fatalf("open since = %v", res.State.OpenSince())
	}
}

// When the meter comes back right after a collector outage the return
// step spans hours; it is still matched to the open fall.
func TestReturnAfterCollectorGap(t *testing.T) {
	steps := []Step{
		step(0, 4195, time.Second, 2358),
		step(time.Second, 2358, 2*time.Hour, 4199),
	}
	res := Run(State{}, steps)
	near(t, "gap", res.Episodes[0].GapKwh, 4)
	near(t, "offset", res.State.Offset(), 0)
}

// More energy coming back than the counter could have metered since the
// fall is not credited: the excess is a seam.
func TestImplausibleGapBecomesSeam(t *testing.T) {
	steps := []Step{
		step(0, 1000, time.Second, 900),
		step(time.Second, 900, 2*time.Second, 51000),
	}
	res := Run(State{}, steps)
	var dropout, seam bool
	for _, e := range res.Episodes {
		switch e.Kind {
		case KindDropout:
			dropout = true
			near(t, "gap", e.GapKwh, 0)
		case KindSeam:
			seam = true
		}
	}
	if !dropout || !seam {
		t.Fatalf("episodes = %+v", res.Episodes)
	}
	near(t, "corrected", corrected(res.Segments, at(2*time.Second), 51000), 1000)
}

// Two devices dropping out one after the other come back newest first.
func TestNestedFalls(t *testing.T) {
	steps := []Step{
		step(0, 3000, time.Second, 2000),
		step(time.Hour, 2000, time.Hour+time.Second, 1500),
		step(2*time.Hour, 1500, 2*time.Hour+time.Second, 2000),
		step(3*time.Hour, 2000, 3*time.Hour+time.Second, 3010),
	}
	res := Run(State{}, steps)
	near(t, "offset", res.State.Offset(), 0)
	var closed int
	for _, e := range res.Episodes {
		if e.Kind == KindDropout && !e.End.IsZero() {
			closed++
		}
	}
	if closed != 2 {
		t.Fatalf("episodes = %+v", res.Episodes)
	}
	near(t, "end", corrected(res.Segments, at(3*time.Hour+time.Second), 3010), 3010)
}

// State carries across runs: a fall seen in one pass is matched by a
// return seen in the next.
func TestStateCarriesAcrossRuns(t *testing.T) {
	first := Run(State{}, []Step{step(0, 24950, time.Second, 8300.41)})
	second := Run(first.State, []Step{step(time.Hour, 8300.41, time.Hour+time.Second, 24951)})
	if len(second.Segments) != 2 {
		t.Fatalf("segments = %+v", second.Segments)
	}
	near(t, "closed gap end", second.Segments[0].EndOffset, 16650.59)
	if !second.Segments[0].Start.Equal(at(time.Second)) {
		t.Fatalf("the open segment from the first run should be the one closed: %+v", second.Segments[0])
	}
}

func TestOrdinaryStepsChangeNothing(t *testing.T) {
	res := Run(State{}, []Step{
		step(0, 100, time.Second, 100.3),
		step(time.Second, 100.3, 2*time.Second, 100.2),
		step(2*time.Second, 100.2, 10*time.Minute, 180),
		step(10*time.Minute, 180, 10*time.Minute+time.Second, 175), // a register resync
	})
	if len(res.Segments) != 0 || len(res.Episodes) != 0 {
		t.Fatalf("res = %+v", res)
	}
}

// A register that publishes five minutes of metering at once is polled
// a second apart; measured from when its reading settled, the step is
// ordinary energy.
func TestCoarseRegisterUpdate(t *testing.T) {
	s := step(5*time.Minute, 1000, 5*time.Minute+time.Second, 1080)
	s.Settled = at(0)
	if res := Run(State{}, []Step{s}); len(res.Episodes) != 0 {
		t.Fatalf("episodes = %+v", res.Episodes)
	}
	// Garbage after a night of standing still is still garbage: the
	// settled time counts for at most CoarseUpdate.
	g := step(10*time.Hour, 1000, 10*time.Hour+time.Second, 29000)
	g.Settled = at(0)
	if res := Run(State{}, []Step{g}); len(res.Episodes) != 1 || res.Episodes[0].Kind != KindSeam {
		t.Fatalf("episodes = %+v", res.Episodes)
	}
}

func TestSegmentRamp(t *testing.T) {
	g := Segment{Start: at(0), End: at(10 * time.Hour), StartOffset: 100, EndOffset: 110}
	near(t, "start", g.At(at(0)), 100)
	near(t, "middle", g.At(at(5*time.Hour)), 105)
	near(t, "end", g.At(at(10*time.Hour)), 110)
	near(t, "before any segment", OffsetAt([]Segment{g}, at(-time.Hour)), 0)
}

func TestFrozenEpisodes(t *testing.T) {
	kyiv, _ := time.LoadLocation("Europe/Kyiv")
	day := func(d int) time.Time { return time.Date(2026, 8, d, 0, 0, 0, 0, kyiv) }
	days := []DaySpan{
		{Day: day(16), First: 100, Last: 120, Samples: 86400},
		{Day: day(17), First: 120, Last: 120, Samples: 86400},
		{Day: day(18), First: 120, Last: 120, Samples: 86400},
		{Day: day(19), First: 120, Last: 120, Samples: 86400},
		{Day: day(20), First: 120, Last: 150, Samples: 86400},
		{Day: day(21), First: 150, Last: 150, Samples: 86400},
		{Day: day(22), First: 150, Last: 150, Samples: 40},
		{Day: day(23), First: 150, Last: 150, Samples: 86400},
		{Day: day(24), First: 150, Last: 150, Samples: 86400},
	}
	eps := FrozenEpisodes(days, 2, 200, true)
	if len(eps) != 2 {
		t.Fatalf("episodes = %+v", eps)
	}
	if !eps[0].Start.Equal(day(17)) || !eps[0].End.Equal(day(20)) {
		t.Fatalf("first = %+v", eps[0])
	}
	if !eps[1].Start.Equal(day(23)) || !eps[1].End.IsZero() {
		t.Fatalf("second should be open: %+v", eps[1])
	}
}
