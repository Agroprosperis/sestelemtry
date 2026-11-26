package offgrid

import (
	"math"
	"sort"
	"time"
)

// Plan against fact for each outage (ems_offgrid_advice.md §9), from the
// stored hours: hour-precise, the power-mode minutes tell what ran.

// FactHour is one stored hour of the site.
type FactHour struct {
	TS            time.Time
	MinGenerator  int
	MinIsland     int
	MinBlackout   int
	MinTransition int
	GenKwh        float64
	FuelL         float64
	FuelUah       float64
	LoadKwh       float64 // load in the hour's off-grid minutes
	EssKwh        float64 // battery to the load off the grid
	PVKwh         float64
	PlanLoadKw    *float64 // the energy manager's plan in force
	SocStartPct   *float64
	BuyUahPerKwh  float64 // all-in import price of the hour
}

func (h FactHour) offgridMin() int {
	return h.MinGenerator + h.MinIsland + h.MinBlackout + h.MinTransition
}

// Record is §9 for one outage, scheduled or not.
type Record struct {
	ScheduledStart   *time.Time `json:"scheduled_start,omitempty"`
	ScheduledEnd     *time.Time `json:"scheduled_end,omitempty"`
	Kind             string     `json:"kind"` // schedule | other | unscheduled
	Happened         bool       `json:"happened"`
	Start            *time.Time `json:"start,omitempty"`
	End              *time.Time `json:"end,omitempty"`
	Sequence         []string   `json:"sequence"`
	MinutesGenerator int        `json:"minutes_generator"`
	MinutesIsland    int        `json:"minutes_island"`
	MinutesBlackout  int        `json:"minutes_blackout"`
	Stops            int        `json:"stops"`
	GenKwh           float64    `json:"gen_kwh"`
	GenBelowMinMin   int        `json:"gen_below_min_minutes"`
	FuelL            float64    `json:"fuel_l"`
	FuelUah          float64    `json:"fuel_uah"`
	EssKwh           float64    `json:"ess_kwh"`
	SocStartPct      *float64   `json:"soc_start_pct,omitempty"`
	SocEndPct        *float64   `json:"soc_end_pct,omitempty"`
	ReserveTargetPct *float64   `json:"reserve_target_pct,omitempty"`
	LoadPlanKwh      *float64   `json:"load_plan_kwh,omitempty"`
	LoadFactKwh      float64    `json:"load_fact_kwh"`
	DeviationPct     *float64   `json:"deviation_pct,omitempty"`
	CostFactUah      float64    `json:"cost_fact_uah"`
	Advised          string     `json:"advised,omitempty"`
	Variants         []Variant  `json:"variants,omitempty"`
}

// Summary is the period's line (§9).
type Summary struct {
	Outages        int     `json:"outages"`
	Unscheduled    int     `json:"unscheduled"`
	HoursOffgrid   float64 `json:"hours_offgrid"`
	FuelL          float64 `json:"fuel_l"`
	FuelUah        float64 `json:"fuel_uah"`
	CostFactUah    float64 `json:"cost_fact_uah"`
	CostAdvisedUah float64 `json:"cost_advised_uah"`
	// UnderestimatePct is the mean shortfall of the plan against the fact
	// over the outages with both; it replaces the 20 % margin once there
	// are ten (§4).
	UnderestimatePct *float64 `json:"underestimate_pct,omitempty"`
	WithPlan         int      `json:"with_plan"`
}

// episode is a run of hours with off-grid minutes.
type episode struct{ from, to int }

func episodes(hours []FactHour) []episode {
	var out []episode
	for i := 0; i < len(hours); i++ {
		if hours[i].offgridMin() == 0 {
			continue
		}
		j := i
		for j+1 < len(hours) && hours[j+1].offgridMin() > 0 && hours[j+1].TS.Sub(hours[j].TS) == time.Hour {
			j++
		}
		out = append(out, episode{i, j})
		i = j
	}
	return out
}

// Records joins the schedule with what ran. Hours must be sorted.
func Records(scheduled []Outage, hours []FactHour, p AdviceParams, marginPct float64) ([]Record, Summary) {
	sort.Slice(hours, func(i, j int) bool { return hours[i].TS.Before(hours[j].TS) })
	eps := episodes(hours)
	used := make([]bool, len(eps))
	var recs []Record
	for _, o := range scheduled {
		s, e := o.Start, o.End
		r := Record{ScheduledStart: &s, ScheduledEnd: &e, Kind: o.Kind}
		best, bestOver := -1, time.Duration(0)
		for k, ep := range eps {
			if used[k] {
				continue
			}
			from, to := hours[ep.from].TS, hours[ep.to].TS.Add(time.Hour)
			if ov := overlap(from, to, s.Add(-time.Hour), e.Add(time.Hour)); ov > bestOver {
				best, bestOver = k, ov
			}
		}
		if best >= 0 {
			used[best] = true
			fill(&r, hours[eps[best].from:eps[best].to+1], p, marginPct)
		}
		recs = append(recs, r)
	}
	for k, ep := range eps {
		if used[k] {
			continue
		}
		r := Record{Kind: "unscheduled"}
		fill(&r, hours[ep.from:ep.to+1], p, marginPct)
		recs = append(recs, r)
	}
	sort.SliceStable(recs, func(i, j int) bool { return recordAt(recs[i]).Before(recordAt(recs[j])) })
	return recs, summarize(recs)
}

func recordAt(r Record) time.Time {
	if r.Start != nil {
		return *r.Start
	}
	if r.ScheduledStart != nil {
		return *r.ScheduledStart
	}
	return time.Time{}
}

// fill measures one run of off-grid hours. The off-grid minutes of the
// first hour are taken at its end, those of the last at its start.
func fill(r *Record, hs []FactHour, p AdviceParams, marginPct float64) {
	r.Happened = true
	first, last := hs[0], hs[len(hs)-1]
	start := first.TS.Add(time.Duration(60-first.offgridMin()) * time.Minute)
	end := last.TS.Add(time.Duration(last.offgridMin()) * time.Minute)
	if len(hs) == 1 {
		start = first.TS
	}
	r.Start, r.End = &start, &end
	var planKwh float64
	planKnown := true
	var factHours, planHours []Hour
	seen := map[string]bool{}
	for _, h := range hs {
		r.MinutesGenerator += h.MinGenerator
		r.MinutesIsland += h.MinIsland
		r.MinutesBlackout += h.MinBlackout
		r.GenKwh += h.GenKwh
		r.FuelL += h.FuelL
		r.FuelUah += h.FuelUah
		r.EssKwh += h.EssKwh
		r.LoadFactKwh += h.LoadKwh
		if h.MinGenerator > 0 && h.GenKwh/(float64(h.MinGenerator)/60) < p.GenMinKw {
			r.GenBelowMinMin += h.MinGenerator
		}
		for _, m := range []struct {
			name string
			min  int
		}{{"знеструмлено", h.MinBlackout}, {"острів", h.MinIsland}, {"генератор", h.MinGenerator}} {
			if m.min > 0 && !seen[m.name] {
				seen[m.name] = true
				r.Sequence = append(r.Sequence, m.name)
			}
		}
		frac := float64(h.offgridMin()) / 60
		if frac > 0 {
			factHours = append(factHours, Hour{From: h.TS, Fraction: frac, LoadKw: h.LoadKwh / frac, PVKw: h.PVKwh / frac})
		}
		if h.PlanLoadKw == nil {
			planKnown = false
		} else {
			planKwh += *h.PlanLoadKw * frac
			planHours = append(planHours, Hour{From: h.TS, Fraction: frac, LoadKw: *h.PlanLoadKw * (1 + marginPct/100)})
		}
	}
	r.Stops = 2
	if r.MinutesIsland > 0 && r.MinutesGenerator > 0 {
		r.Stops = 3
	}
	r.SocStartPct = first.SocStartPct
	if r.SocStartPct != nil && p.CapacityKwh > 0 {
		r.SocEndPct = ptr(round1(*r.SocStartPct - r.EssKwh/p.CapacityKwh*100))
	}
	if planKnown && len(planHours) > 0 {
		r.LoadPlanKwh = ptr(round1(planKwh))
		if planKwh > 0 {
			r.DeviationPct = ptr(round1((r.LoadFactKwh - planKwh) / planKwh * 100))
		}
		need := planKwh * (1 + marginPct/100)
		r.ReserveTargetPct = ptr(round1(math.Min(90, p.FloorPct+need/p.CapacityKwh*100)))
	}
	r.CostFactUah = math.Round(r.FuelUah + r.EssKwh*p.RechargeUahPerKwh)
	if r.SocStartPct != nil {
		if len(planHours) > 0 && planKnown {
			r.Advised = Advise(planHours, *r.SocStartPct, p).Recommended
		}
		r.Variants = Advise(factHours, *r.SocStartPct, p).Variants
	}
	r.GenKwh, r.FuelL, r.FuelUah = round1(r.GenKwh), round1(r.FuelL), math.Round(r.FuelUah)
	r.EssKwh, r.LoadFactKwh = round1(r.EssKwh), round1(r.LoadFactKwh)
}

func summarize(recs []Record) Summary {
	var s Summary
	var under []float64
	for _, r := range recs {
		if !r.Happened {
			continue
		}
		s.Outages++
		if r.Kind == "unscheduled" {
			s.Unscheduled++
		}
		s.HoursOffgrid += float64(r.MinutesGenerator+r.MinutesIsland+r.MinutesBlackout) / 60
		s.FuelL += r.FuelL
		s.FuelUah += r.FuelUah
		s.CostFactUah += r.CostFactUah
		for _, v := range r.Variants {
			if v.ID == r.Advised && v.CostUah != nil {
				s.CostAdvisedUah += *v.CostUah
			}
		}
		if r.DeviationPct != nil {
			under = append(under, *r.DeviationPct)
		}
	}
	s.HoursOffgrid, s.FuelL = round1(s.HoursOffgrid), round1(s.FuelL)
	s.WithPlan = len(under)
	if len(under) > 0 {
		sum := 0.0
		for _, d := range under {
			sum += d
		}
		s.UnderestimatePct = ptr(round1(sum / float64(len(under))))
	}
	return s
}

// MarginFromFact is §4: after ten outages with plan and fact the margin
// is the plan's mean underestimate, but not below 10 %; before, the
// configured one.
func MarginFromFact(s Summary, configured float64) float64 {
	if s.WithPlan < 10 || s.UnderestimatePct == nil {
		return configured
	}
	return math.Max(10, *s.UnderestimatePct)
}
