package energyflow

import (
	"math"
	"sort"
	"time"
)

// SupplyMode is how a site was fed during one allocation window, read
// from the generator controller's breakers, power and bus voltage
// (ems-spec generator handoff §4.1).
type SupplyMode string

const (
	// SupplyNone marks a window without generator data at all: before
	// the organization's first generator sample, or a site without one.
	// Such windows are computed exactly as they were before.
	SupplyNone       SupplyMode = ""
	SupplyGrid       SupplyMode = "grid"
	SupplyGenerator  SupplyMode = "generator"
	SupplyIsland     SupplyMode = "island"
	SupplyBlackout   SupplyMode = "blackout"
	SupplyTransition SupplyMode = "transition"
	SupplyUnknown    SupplyMode = "unknown"
)

// Offgrid reports the modes in which the grid was not connected, so the
// SmartLogger grid meter neither imports nor exports.
func (m SupplyMode) Offgrid() bool {
	return m == SupplyGenerator || m == SupplyIsland || m == SupplyBlackout
}

// Supply thresholds of handoff §4.1.
const (
	busLiveVolts   = 180.0
	genRunningKw   = 5.0
	genStaleAfter  = 30 * time.Second
	genMaxPowerGap = 2 * time.Minute // a longer silence adds nothing to the power integral
)

// GenWindow is the generator state of one allocation window.
type GenWindow struct {
	Mode SupplyMode
	// GenKwh is the generator energy attributed to the window.
	GenKwh float64
}

// GenHour sums one clock hour of a timeline.
type GenHour struct {
	Start time.Time
	// GenKwh is the hour's generator energy: the meter delta, or the
	// power integral when the meter could not be trusted (Estimated).
	GenKwh    float64
	Estimated bool
	Minutes   map[SupplyMode]int
}

// GenTimeline is the per-window generator state of a span, built once
// from the generator samples and read by the allocation walk.
type GenTimeline struct {
	window  time.Duration
	windows map[int64]GenWindow // keyed by window end, UnixNano
	hours   map[int64]*GenHour  // keyed by UTC hour start, UnixNano
}

type genPoint struct {
	t time.Time
	v float64
}

// BuildGenTimeline reads the generator samples (Role == RoleGen) of the
// span (from, to] into per-window state. since is the organization's
// first generator sample ever: windows ending at or before it stay
// SupplyNone. A zero since falls back to the first sample given. The
// samples may come in any order. It returns nil when there are none.
//
// Per window the mode follows the latest readings, which must be no
// older than 30 s at the window's end (else SupplyUnknown). Per clock
// hour the generator energy is the stitched meter's delta, spread over
// the hour's windows in proportion to the power integral (§4.2); with
// no usable meter it is the power integral itself.
func BuildGenTimeline(samples []RawSample, from, to time.Time, window time.Duration, since time.Time) *GenTimeline {
	if window <= 0 {
		window = time.Minute
	}
	series := map[string][]genPoint{}
	for _, s := range samples {
		if s.Role != RoleGen {
			continue
		}
		for k, v := range s.Values {
			if isFiniteFloat(v) {
				series[k] = append(series[k], genPoint{t: s.Time, v: v})
			}
		}
	}
	if len(series) == 0 {
		return nil
	}
	first := time.Time{}
	for _, pts := range series {
		sort.Slice(pts, func(i, j int) bool { return pts[i].t.Before(pts[j].t) })
		if first.IsZero() || pts[0].t.Before(first) {
			first = pts[0].t
		}
	}
	if since.IsZero() || first.Before(since) {
		since = first
	}

	tl := &GenTimeline{
		window:  window,
		windows: map[int64]GenWindow{},
		hours:   map[int64]*GenHour{},
	}
	firstEnd := bucketEnd(from.Add(time.Nanosecond), window)
	lastEnd := bucketEnd(to, window)

	grid := newCursor(series[SrcGridBreakersOnBits])
	gen := newCursor(series[SrcGenBreakersOnBits])
	power := newCursor(series[SrcActiveGenPowerKw])
	bus := newCursor(series[SrcGenBusVoltageAV])
	var ends []time.Time
	for end := firstEnd; !end.After(lastEnd); end = end.Add(window) {
		if !end.After(since) {
			continue
		}
		mode := supplyModeAt(end, grid, gen, power, bus)
		tl.windows[end.UnixNano()] = GenWindow{Mode: mode}
		ends = append(ends, end)
		h := tl.hourOf(end)
		h.Minutes[mode] += int(window / time.Minute)
	}
	if len(ends) == 0 {
		return tl
	}

	integral := powerIntegralByWindow(series[SrcActiveGenPowerKw], window)
	byHour := map[int64][]time.Time{}
	for _, end := range ends {
		k := hourKey(end)
		byHour[k] = append(byHour[k], end)
	}
	counter := series[SrcAccumulatedGenEnergyKwh]
	alive := series[SrcGenMeterAlive]
	for k, hourEnds := range byHour {
		h := tl.hours[k]
		var hourIntegral float64
		for _, end := range hourEnds {
			hourIntegral += integral[end.UnixNano()]
		}
		kwh, ok := meterDelta(counter, alive, h.Start, h.Start.Add(time.Hour))
		if !ok {
			kwh = hourIntegral
			h.Estimated = hourIntegral > 0
		}
		h.GenKwh = kwh
		spreadGen(tl, hourEnds, integral, hourIntegral, kwh)
	}
	return tl
}

// supplyModeAt applies the handoff §4.1 table to the readings in force
// at the window's end.
func supplyModeAt(end time.Time, grid, gen, power, bus *cursor) SupplyMode {
	gridBits, gridOK := grid.freshAt(end)
	genBits, genOK := gen.freshAt(end)
	if !gridOK || !genOK {
		return SupplyUnknown
	}
	kw, _ := power.freshAt(end)
	volts, _ := bus.freshAt(end)
	gridOn := gridBits > 0
	genOn := genBits > 0 || kw > genRunningKw
	switch {
	case gridOn && genOn:
		return SupplyTransition
	case gridOn:
		return SupplyGrid
	case genOn:
		return SupplyGenerator
	case volts > busLiveVolts:
		return SupplyIsland
	default:
		return SupplyBlackout
	}
}

// powerIntegralByWindow integrates the generator power (trapezoid,
// negative power counted as zero) and books each segment on the window
// holding its end. Gaps longer than genMaxPowerGap add nothing.
func powerIntegralByWindow(pts []genPoint, window time.Duration) map[int64]float64 {
	out := map[int64]float64{}
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		dt := b.t.Sub(a.t)
		if dt <= 0 || dt > genMaxPowerGap {
			continue
		}
		kw := (math.Max(a.v, 0) + math.Max(b.v, 0)) / 2
		out[bucketEnd(b.t, window).UnixNano()] += kw * dt.Hours()
	}
	return out
}

// meterDelta is the generator meter's energy over the hour (start, end]:
// the last reading by the hour's end minus the last before its start,
// or minus the hour's first reading when nothing earlier was loaded.
// It reports false when the meter cannot be trusted for the hour: no
// reading inside it, a reading while the meter was offline
// (gen_meter_alive ≠ 1), or a negative delta the stitcher did not mend.
func meterDelta(counter, alive []genPoint, start, end time.Time) (float64, bool) {
	for _, a := range alive {
		if a.t.After(start) && !a.t.After(end) && a.v != 1 {
			return 0, false
		}
	}
	var base, last *genPoint
	for i := range counter {
		p := &counter[i]
		switch {
		case !p.t.After(start):
			base = p
		case !p.t.After(end):
			if base == nil {
				base = p
			}
			last = p
		}
	}
	if last == nil {
		return 0, false
	}
	d := last.v - base.v
	if d < 0 {
		return 0, false
	}
	return d, true
}

// spreadGen books the hour's generator energy on its windows: by power
// integral, else evenly on the windows running on the generator, else
// all on the hour's first window.
func spreadGen(tl *GenTimeline, ends []time.Time, integral map[int64]float64, hourIntegral, kwh float64) {
	if kwh <= 0 {
		return
	}
	sort.Slice(ends, func(i, j int) bool { return ends[i].Before(ends[j]) })
	add := func(end time.Time, v float64) {
		k := end.UnixNano()
		w := tl.windows[k]
		w.GenKwh += v
		tl.windows[k] = w
	}
	if hourIntegral > 0 {
		for _, end := range ends {
			if p := integral[end.UnixNano()]; p > 0 {
				add(end, kwh*p/hourIntegral)
			}
		}
		return
	}
	var running []time.Time
	for _, end := range ends {
		if tl.windows[end.UnixNano()].Mode == SupplyGenerator {
			running = append(running, end)
		}
	}
	if len(running) == 0 {
		add(ends[0], kwh)
		return
	}
	for _, end := range running {
		add(end, kwh/float64(len(running)))
	}
}

func (tl *GenTimeline) hourOf(end time.Time) *GenHour {
	k := hourKey(end)
	h, ok := tl.hours[k]
	if !ok {
		h = &GenHour{Start: time.Unix(0, k).UTC(), Minutes: map[SupplyMode]int{}}
		tl.hours[k] = h
	}
	return h
}

// hourKey is the UTC clock hour a window ending at end belongs to: the
// windows of an hour end in (start, start+1h].
func hourKey(end time.Time) int64 {
	return end.UTC().Add(-time.Nanosecond).Truncate(time.Hour).UnixNano()
}

// Window returns the state of the window ending at end; false for a
// window without generator data.
func (tl *GenTimeline) Window(end time.Time) (GenWindow, bool) {
	if tl == nil {
		return GenWindow{}, false
	}
	w, ok := tl.windows[end.UnixNano()]
	return w, ok
}

// GenBetween sums the generator energy of the windows ending in
// (after, upTo]: an interval longer than one window spans several.
func (tl *GenTimeline) GenBetween(after, upTo time.Time) float64 {
	if tl == nil {
		return 0
	}
	var sum float64
	for end := upTo; end.After(after); end = end.Add(-tl.window) {
		sum += tl.windows[end.UnixNano()].GenKwh
	}
	return sum
}

// Hour returns the totals of the UTC clock hour starting at start.
func (tl *GenTimeline) Hour(start time.Time) (GenHour, bool) {
	if tl == nil {
		return GenHour{}, false
	}
	h, ok := tl.hours[start.UTC().UnixNano()]
	if !ok {
		return GenHour{}, false
	}
	return *h, true
}

// cursor walks one time-sorted series forward to find the reading in
// force at successive instants.
type cursor struct {
	pts []genPoint
	i   int
}

func newCursor(pts []genPoint) *cursor { return &cursor{pts: pts, i: -1} }

// freshAt returns the latest reading at or before t, if it is no older
// than genStaleAfter. Calls must come with non-decreasing t.
func (c *cursor) freshAt(t time.Time) (float64, bool) {
	for c.i+1 < len(c.pts) && !c.pts[c.i+1].t.After(t) {
		c.i++
	}
	if c.i < 0 {
		return 0, false
	}
	p := c.pts[c.i]
	if t.Sub(p.t) > genStaleAfter {
		return 0, false
	}
	return p.v, true
}
