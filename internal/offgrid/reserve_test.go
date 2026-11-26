package offgrid

import (
	"testing"
	"time"
)

var zeParams = Params{CapacityKwh: 1720, FloorPct: 20, MaxPct: 90, ErrorMarginPct: 20}

func at(h, m int) time.Time { return time.Date(2026, 11, 27, h, m, 0, 0, time.UTC) }

func flat(kw float64, src string) Loads {
	return func(time.Time) (float64, string, bool) { return kw, src, true }
}

// §5: 2 h at 300 kW planned, +20 %: 720 kWh → 20 % + 41.9 % = 61.9 %.
func TestReserveFromThePlan(t *testing.T) {
	r := Reserves([]Outage{{Start: at(10, 0), End: at(12, 0), Kind: "schedule"}}, flat(300, SourcePlan), zeParams)[0]
	if r.Source != SourcePlan || r.NeedKwh == nil || *r.NeedKwh != 720 || r.TargetPct != 61.9 || r.Short {
		t.Fatalf("reserve %+v", r)
	}
	if len(r.Hours) != 2 || r.Hours[0].Fraction != 1 {
		t.Fatalf("hours %+v", r.Hours)
	}
}

// A half-hour edge counts half; the plan and the average mixed.
func TestReservePartialHoursAndMixedSources(t *testing.T) {
	loads := func(h time.Time) (float64, string, bool) {
		if h.Hour() == 10 {
			return 200, SourcePlan, true
		}
		return 100, SourceAvg7d, true
	}
	r := Reserves([]Outage{{Start: at(10, 30), End: at(11, 30)}}, loads, zeParams)[0]
	// 0.5 h × 200 × 1.2 + 0.5 h × 100 × 1.2 = 180 kWh → 20 + 10.5 = 30.5 %.
	if r.Source != SourceMixed || *r.NeedKwh != 180 || r.TargetPct != 30.5 {
		t.Fatalf("reserve %+v", r)
	}
}

// §4 п. 3: no estimate for an hour — no calculation, the reserve is 90 %.
func TestReserveWithoutAnEstimate(t *testing.T) {
	loads := func(h time.Time) (float64, string, bool) { return 0, "", h.Hour() != 11 }
	r := Reserves([]Outage{{Start: at(10, 0), End: at(12, 0)}}, loads, zeParams)[0]
	if r.Source != SourceNone || r.NeedKwh != nil || r.TargetPct != 90 {
		t.Fatalf("reserve %+v", r)
	}
}

// §5: more than 70 % of the capacity (1 204 kWh) — 90 % and the time the
// battery runs out.
func TestReserveShort(t *testing.T) {
	r := Reserves([]Outage{{Start: at(8, 0), End: at(14, 0)}}, flat(500, SourcePlan), zeParams)[0]
	// 600 kW with the margin; 1 204 kWh last 2 h 0.4 min → 10:00.
	if !r.Short || r.TargetPct != 90 || r.RunsOutAt == nil || !r.RunsOutAt.Equal(at(10, 0)) {
		t.Fatalf("reserve %+v (runs out %v)", r, r.RunsOutAt)
	}
}

func TestHourlyAverage(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Kyiv")
	samples := map[time.Time]float64{
		time.Date(2026, 11, 20, 8, 0, 0, 0, loc).UTC(): 200,
		time.Date(2026, 11, 21, 8, 0, 0, 0, loc).UTC(): 300,
		time.Date(2026, 11, 21, 9, 0, 0, 0, loc).UTC(): 100,
	}
	avg := HourlyAverage(samples, loc)
	if avg[8] != 250 || avg[9] != 100 || len(avg) != 2 {
		t.Fatalf("average %v", avg)
	}
}
