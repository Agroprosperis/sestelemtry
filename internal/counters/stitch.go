// Package counters keeps the SmartLogger lifetime energy counters
// continuous across device dropouts.
//
// The plant-level registers (PV yield, grid import / export, ESS charge /
// discharge) are sums the SmartLogger builds over the devices it can
// reach. When it loses one — the grid meter, an inverter — the sum falls
// by that device's lifetime total or freezes on a stale reading, and
// jumps back once the link returns. Read as energy, the way back up
// books phantom MWh; past the delta pipeline's power ceiling it is
// dropped instead, together with the real energy the device metered
// while it was gone.
//
// Run walks the discontinuities of one counter and keeps an offset such
// that raw + offset stays continuous: a fall is bridged, a return is
// matched against the falls still open, and whatever the returning
// device metered meanwhile is spread evenly across the gap. A forward
// jump nothing explains (a seam between two data sources, a corrupted
// high reading) is removed; a fall that cancels it soon after closes it
// as a spike.
package counters

import (
	"math"
	"sort"
	"time"
)

// Metrics lists the lifetime accumulators that are stitched. The
// *_day_kwh registers reset every midnight by design and stay as read.
var Metrics = []string{
	"accumulated_pv_energy_yield_kwh",
	"accumulated_electricity_purchased_kwh",
	"accumulated_electricity_sold_kwh",
	"total_energy_charged_kwh",
	"total_energy_discharged_kwh",
}

// IsStitched reports whether metricKey is one of Metrics.
func IsStitched(metricKey string) bool {
	for _, m := range Metrics {
		if m == metricKey {
			return true
		}
	}
	return false
}

const (
	// DropTolKwh is the smallest backward step treated as a fall. A
	// device leaving the sum takes thousands of kWh with it; a few kWh
	// back is the register resyncing, which the delta queries clamp.
	DropTolKwh = 10.0
	// MaxPowerKw bounds what one counter can meter. A forward step faster
	// than this is a discontinuity, not energy; elevator sites run well
	// under 2 MW.
	MaxPowerKw = 5000.0
	// SlackKwh absorbs register quantisation on one-second steps.
	SlackKwh = 2.0
	// CandidateFloorKwh is the forward step always handed to Run whatever
	// its elapsed time, so a return that lands right after a collector
	// gap is still recognised.
	CandidateFloorKwh = 50.0
	// SpikeWindow is how long an unexplained forward jump waits for the
	// matching fall that would make it a spike.
	SpikeWindow = 24 * time.Hour
	// CoarseUpdate is the longest a register may hold one reading and
	// then publish everything it metered meanwhile in one step. Polled
	// every second, such a step looks impossibly fast unless it is
	// measured from when the reading settled.
	CoarseUpdate = 15 * time.Minute
)

// ForwardLimitKwh is the largest forward step a counter can make in dt.
func ForwardLimitKwh(dt time.Duration) float64 {
	if dt < 0 {
		dt = 0
	}
	return MaxPowerKw*dt.Hours() + SlackKwh
}

// Step is one sample-to-sample transition of a counter.
type Step struct {
	PrevTime time.Time
	Prev     float64
	Time     time.Time
	Value    float64
	// Settled is when the counter first showed Prev; zero means
	// PrevTime.
	Settled time.Time
}

// Duration is the time the step's energy could have accumulated over:
// since the previous sample, or since the previous reading settled when
// that is longer, up to CoarseUpdate.
func (s Step) Duration() time.Duration {
	dt := s.Time.Sub(s.PrevTime)
	if s.Settled.IsZero() {
		return dt
	}
	held := s.Time.Sub(s.Settled)
	if held > CoarseUpdate {
		held = CoarseUpdate
	}
	if held > dt {
		return held
	}
	return dt
}

// Segment is the offset in force over [Start, End). It runs linearly
// from StartOffset to EndOffset, which is how energy credited to a gap
// spreads over it. The segment still open has a zero End and holds
// StartOffset.
type Segment struct {
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	StartOffset float64   `json:"start_offset"`
	EndOffset   float64   `json:"end_offset"`
}

// At returns the segment's offset at t.
func (g Segment) At(t time.Time) float64 {
	if g.End.IsZero() || !g.End.After(g.Start) || !t.After(g.Start) {
		return g.StartOffset
	}
	if !t.Before(g.End) {
		return g.EndOffset
	}
	frac := float64(t.Sub(g.Start)) / float64(g.End.Sub(g.Start))
	return g.StartOffset + (g.EndOffset-g.StartOffset)*frac
}

// OffsetAt returns the offset at t from segments sorted by Start. Before
// the first segment the counter is uncorrected.
func OffsetAt(segs []Segment, t time.Time) float64 {
	i := sort.Search(len(segs), func(i int) bool { return segs[i].Start.After(t) })
	if i == 0 {
		return 0
	}
	return segs[i-1].At(t)
}

// Kind classifies an Episode.
type Kind string

const (
	// KindDropout: the counter fell — a device left the sum or its
	// reading went stale. Open until the counter returns.
	KindDropout Kind = "dropout"
	// KindSeam: a forward jump nothing explains, removed.
	KindSeam Kind = "seam"
	// KindSpike: a seam cancelled by a matching fall soon after.
	KindSpike Kind = "spike"
	// KindFrozen: the counter stood still for days while telemetry kept
	// arriving. Found from the daily aggregate, see FrozenEpisodes.
	KindFrozen Kind = "frozen"
)

// Episode describes one discontinuity, for day flags and alerts.
type Episode struct {
	Kind  Kind
	Start time.Time
	// End is zero while the episode is open.
	End time.Time
	// Before and After are the raw readings either side of the step
	// that opened the episode.
	Before float64
	After  float64
	// Returned is the raw reading the counter came back to.
	Returned float64
	// GapKwh is the energy credited across a closed dropout.
	GapKwh float64
}

// fall is one drop of the counter that has not come back yet.
type fall struct {
	At     time.Time `json:"at"`
	Kwh    float64   `json:"kwh"`
	Before float64   `json:"before"`
	After  float64   `json:"after"`
}

// jump is the latest unexplained forward step, kept for SpikeWindow.
type jump struct {
	At     time.Time `json:"at"`
	Kwh    float64   `json:"kwh"`
	Before float64   `json:"before"`
	After  float64   `json:"after"`
}

// State carries the stitcher from one Run to the next. The zero value
// starts an uncorrected counter.
type State struct {
	// Base is the permanent part of the offset: the seams removed.
	Base  float64 `json:"base"`
	Falls []fall  `json:"falls,omitempty"`
	Jump  *jump   `json:"jump,omitempty"`
	// Open is the segment in force; its Start is zero before the first
	// discontinuity.
	Open Segment `json:"open"`
}

func (s *State) pending() float64 {
	var p float64
	for _, f := range s.Falls {
		p += f.Kwh
	}
	return p
}

// Offset is the correction currently in force.
func (s *State) Offset() float64 { return s.Base + s.pending() }

// OpenSince is when the oldest unreturned fall happened; zero when the
// counter is whole.
func (s *State) OpenSince() time.Time {
	if len(s.Falls) == 0 {
		return time.Time{}
	}
	return s.Falls[0].At
}

// Result is what one Run changed. Segments and Episodes are keyed by
// Start: an entry replaces whatever was stored under the same Start,
// which is how the previously open segment gets closed and an open
// dropout gets its end.
type Result struct {
	Segments []Segment
	Episodes []Episode
	State    State
}

// Run feeds steps, in time order, through the stitcher. Steps that are
// neither a fall nor a discontinuous jump pass through unchanged, so
// callers may hand over just the candidates (see CandidateFloorKwh).
func Run(st State, steps []Step) Result {
	var res Result
	for _, s := range steps {
		if st.Jump != nil && s.Time.Sub(st.Jump.At) > SpikeWindow {
			st.Jump = nil
		}
		d := s.Value - s.Prev
		dt := s.Duration()
		switch {
		case d < -DropTolKwh:
			st.onFall(&res, s, -d)
		case d > 0 && st.pending() > 0 && (d > ForwardLimitKwh(dt) || d >= st.pending()/2):
			st.onReturn(&res, s, d)
		case d > ForwardLimitKwh(dt):
			st.onJump(&res, s, d)
		}
	}
	res.State = st
	return res
}

// onFall bridges a drop. One that undoes the latest unexplained jump
// closes it as a spike instead: the reading went stale above the real
// counter and came back down. The real counter kept metering meanwhile,
// so the fall may undo less than the jump by up to what it could have
// metered; that difference is credited across the spike like a gap.
func (st *State) onFall(res *Result, s Step, kwh float64) {
	if j := st.Jump; j != nil {
		tol := math.Max(SlackKwh, 0.02*j.Kwh)
		metered := ForwardLimitKwh(s.Time.Sub(j.At))
		if kwh <= j.Kwh+tol && kwh >= j.Kwh-metered-tol {
			gap := math.Max(j.Kwh-kwh, 0)
			st.Base += kwh + gap
			st.Jump = nil
			res.addEpisode(Episode{Kind: KindSpike, Start: j.At, End: s.Time, Before: j.Before, After: j.After, Returned: s.Value, GapKwh: gap})
			st.switchAt(res, s.Time, st.Open.StartOffset+gap)
			return
		}
	}
	st.Falls = append(st.Falls, fall{At: s.Time, Kwh: kwh, Before: s.Prev, After: s.Value})
	res.addEpisode(Episode{Kind: KindDropout, Start: s.Time, Before: s.Prev, After: s.Value})
	st.switchAt(res, s.Time, st.Open.StartOffset)
}

// onReturn matches a forward jump against the open falls, newest first.
// What the jump brings beyond them is the energy the missing device
// metered while it was gone; it is credited as a ramp over the open
// segment unless it is more than the counter could physically have
// metered since the oldest fall it closes.
func (st *State) onReturn(res *Result, s Step, kwh float64) {
	returned := math.Min(kwh, st.pending())
	gap := kwh - returned
	left := returned
	var closed []fall
	for left > 1e-9 && len(st.Falls) > 0 {
		i := len(st.Falls) - 1
		f := st.Falls[i]
		if f.Kwh <= left+1e-9 {
			left -= f.Kwh
			st.Falls = st.Falls[:i]
			closed = append(closed, f)
			continue
		}
		st.Falls[i].Kwh -= left
		left = 0
	}
	if gap > 0 {
		since := s.PrevTime
		if len(closed) > 0 {
			since = closed[len(closed)-1].At
		}
		if gap > ForwardLimitKwh(s.Time.Sub(since)) {
			st.Base -= gap
			res.addEpisode(Episode{Kind: KindSeam, Start: s.Time, End: s.Time, Before: s.Prev, After: s.Value})
			gap = 0
		}
	}
	for i, f := range closed {
		ep := Episode{Kind: KindDropout, Start: f.At, End: s.Time, Before: f.Before, After: f.After, Returned: s.Value}
		if i == len(closed)-1 {
			ep.GapKwh = gap
		}
		res.addEpisode(ep)
	}
	st.switchAt(res, s.Time, st.Open.StartOffset+gap)
}

func (st *State) onJump(res *Result, s Step, kwh float64) {
	st.Base -= kwh
	st.Jump = &jump{At: s.Time, Kwh: kwh, Before: s.Prev, After: s.Value}
	res.addEpisode(Episode{Kind: KindSeam, Start: s.Time, End: s.Time, Before: s.Prev, After: s.Value})
	st.switchAt(res, s.Time, st.Open.StartOffset)
}

// switchAt closes the open segment at t, ending on endOffset, and opens
// the next one on the offset now in force.
func (st *State) switchAt(res *Result, t time.Time, endOffset float64) {
	if !st.Open.Start.IsZero() && t.After(st.Open.Start) {
		closed := st.Open
		closed.End = t
		closed.EndOffset = endOffset
		res.addSegment(closed)
	}
	st.Open = Segment{Start: t, StartOffset: st.Offset()}
	res.addSegment(st.Open)
}

func (r *Result) addSegment(g Segment) {
	for i := range r.Segments {
		if r.Segments[i].Start.Equal(g.Start) {
			r.Segments[i] = g
			return
		}
	}
	r.Segments = append(r.Segments, g)
}

func (r *Result) addEpisode(e Episode) {
	for i := range r.Episodes {
		if r.Episodes[i].Start.Equal(e.Start) {
			r.Episodes[i] = e
			return
		}
	}
	r.Episodes = append(r.Episodes, e)
}

// DaySpan is one civil day of a counter: its first and last reading and
// how many samples the day held.
type DaySpan struct {
	Day     time.Time
	First   float64
	Last    float64
	Samples int64
}

// frozenMoveKwh is the movement below which a day counts as standing
// still; the registers resolve 0.01 kWh.
const frozenMoveKwh = 0.05

// FrozenEpisodes groups runs of at least minDays consecutive days (in
// order, one per civil day) on which the counter did not move although
// at least minSamples readings arrived. A run that reaches the last day
// is left open when openEnd is set, since it may still be going on.
func FrozenEpisodes(days []DaySpan, minDays int, minSamples int64, openEnd bool) []Episode {
	var out []Episode
	start := -1
	flush := func(end int) {
		if start < 0 || end-start < minDays {
			return
		}
		ep := Episode{
			Kind:   KindFrozen,
			Start:  days[start].Day,
			Before: days[start].First,
			After:  days[end-1].Last,
		}
		if !(openEnd && end == len(days)) {
			ep.End = days[end-1].Day.AddDate(0, 0, 1)
		}
		out = append(out, ep)
	}
	for i, d := range days {
		still := d.Samples >= minSamples && math.Abs(d.Last-d.First) < frozenMoveKwh
		contiguous := start >= 0 && days[i-1].Day.AddDate(0, 0, 1).Equal(d.Day)
		switch {
		case still && start >= 0 && contiguous:
		case still:
			flush(i)
			start = i
		default:
			flush(i)
			start = -1
		}
	}
	flush(len(days))
	return out
}
