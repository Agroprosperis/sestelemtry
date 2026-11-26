// Package offgrid is the cloud side of work without the grid
// (ems-spec ems_offgrid_advice.md): the charge to hold for each outage
// of the schedule, the advice when the grid goes, the power at hand,
// plan against fact. Everything here is arithmetic and hints; nothing
// is written to the loggers or the Encombi.
package offgrid

import (
	"math"
	"time"
)

// Where an hour's expected load comes from (§4).
const (
	SourcePlan  = "plan"  // the energy manager's planned consumption
	SourceAvg7d = "avg7d" // the same hour of the last 7 days, measured
	SourceNone  = "none"  // neither: no estimate
	SourceMixed = "mixed" // the outage's hours come from both
)

// Params are the site's figures (§12).
type Params struct {
	CapacityKwh float64
	// FloorPct is the lowest charge without the grid; MaxPct caps the
	// reserve.
	FloorPct float64
	MaxPct   float64
	// ErrorMarginPct is added to the planned load: plans run short.
	ErrorMarginPct float64
}

// Outage is one stretch of the schedule.
type Outage struct {
	Start time.Time
	End   time.Time
	Kind  string
}

// Loads gives an hour's expected load, kW, and where it came from;
// false when nothing is known.
type Loads func(hour time.Time) (kw float64, source string, ok bool)

// HourNeed is one hour of an outage.
type HourNeed struct {
	TS     time.Time `json:"ts"`
	LoadKw *float64  `json:"load_kw"`
	Source string    `json:"source"`
	// Fraction is the share of the hour inside the outage.
	Fraction float64 `json:"fraction"`
}

// Reserve is §5 for one outage: the charge to have when it starts.
type Reserve struct {
	Start          time.Time  `json:"start"`
	End            time.Time  `json:"end"`
	Kind           string     `json:"kind"`
	Hours          []HourNeed `json:"hours"`
	Source         string     `json:"source"`
	ErrorMarginPct float64    `json:"error_margin_pct"`
	// NeedKwh is the expected load over the outage with the margin;
	// nil when some hour has no estimate.
	NeedKwh   *float64 `json:"need_kwh"`
	TargetPct float64  `json:"target_pct"`
	// Short: the battery does not cover the outage; RunsOutAt is when
	// it reaches the floor from the full reserve.
	Short     bool       `json:"short"`
	RunsOutAt *time.Time `json:"runs_out_at,omitempty"`
}

// Reserves computes §5 for each outage. The sun is left out on purpose:
// the reserve must hold without it.
func Reserves(outages []Outage, loads Loads, p Params) []Reserve {
	out := make([]Reserve, 0, len(outages))
	for _, o := range outages {
		out = append(out, reserveFor(o, loads, p))
	}
	return out
}

func reserveFor(o Outage, loads Loads, p Params) Reserve {
	r := Reserve{Start: o.Start, End: o.End, Kind: o.Kind, ErrorMarginPct: p.ErrorMarginPct, TargetPct: p.MaxPct}
	k := 1 + p.ErrorMarginPct/100
	need, known, sources := 0.0, true, map[string]bool{}
	for h := o.Start.Truncate(time.Hour); h.Before(o.End); h = h.Add(time.Hour) {
		frac := overlap(h, h.Add(time.Hour), o.Start, o.End).Hours()
		hn := HourNeed{TS: h, Fraction: round3(frac), Source: SourceNone}
		if kw, src, ok := loads(h); ok {
			hn.LoadKw, hn.Source = ptr(round1(kw)), src
			need += kw * k * frac
			sources[src] = true
		} else {
			known = false
		}
		r.Hours = append(r.Hours, hn)
	}
	switch {
	case !known || len(r.Hours) == 0:
		r.Source = SourceNone
		return r
	case len(sources) > 1:
		r.Source = SourceMixed
	default:
		for s := range sources {
			r.Source = s
		}
	}
	r.NeedKwh = ptr(round1(need))
	if p.CapacityKwh <= 0 {
		return r
	}
	r.TargetPct = round1(math.Min(p.MaxPct, p.FloorPct+need/p.CapacityKwh*100))
	usable := (p.MaxPct - p.FloorPct) / 100 * p.CapacityKwh
	if need > usable {
		r.Short = true
		r.RunsOutAt = runsOut(r.Hours, o, k, usable)
	}
	return r
}

// runsOut walks the outage until the usable energy is spent.
func runsOut(hours []HourNeed, o Outage, k, usable float64) *time.Time {
	left := usable
	for _, h := range hours {
		from := maxTime(h.TS, o.Start)
		to := minTime(h.TS.Add(time.Hour), o.End)
		kw := *h.LoadKw * k
		e := kw * to.Sub(from).Hours()
		if e >= left && kw > 0 {
			at := from.Add(time.Duration(left / kw * float64(time.Hour))).Truncate(time.Minute)
			return &at
		}
		left -= e
	}
	return nil
}

func overlap(a0, a1, b0, b1 time.Time) time.Duration {
	from, to := maxTime(a0, b0), minTime(a1, b1)
	if !to.After(from) {
		return 0
	}
	return to.Sub(from)
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func ptr(v float64) *float64 { return &v }

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// HourlyAverage is the 7-day average of each local hour of the day,
// from measured hourly loads (kWh in the hour = mean kW).
func HourlyAverage(samples map[time.Time]float64, loc *time.Location) map[int]float64 {
	sum, n := map[int]float64{}, map[int]int{}
	for ts, v := range samples {
		h := ts.In(loc).Hour()
		sum[h] += v
		n[h]++
	}
	out := make(map[int]float64, len(sum))
	for h, s := range sum {
		out[h] = s / float64(n[h])
	}
	return out
}
