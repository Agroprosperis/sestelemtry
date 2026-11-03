package pvplan

import (
	"math"
	"testing"
	"time"
)

func pdeStation() Station {
	return Station{
		OrganizationID: "pde",
		Source:         SourceModel,
		PacLimitKw:     250,
		GammaPctPerC:   -0.28,
		Orientations: []Orientation{
			{Idx: 1, TiltDeg: 30, AzimuthDeg: -13.3, ModulesCount: 504, ModulePowerW: 620, Enabled: true},
		},
	}
}

// The n8n flow stored this hour for PE (2026-09-21, hour_ending 13):
// GTI 814.8 W/m², 24.9 °C, γ −0.28 %/°C, 312.48 kWp → 212.875 kWh. The
// in-house model must land on the same number for the same weather, or
// switching a station would move its plan for reasons that aren't the
// weather.
func TestPlanHoursMatchesTheN8nFlow(t *testing.T) {
	start := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	got := PlanHours(pdeStation(), [][]HourInput{{{Start: start, TempC: 24.9, GtiWm2: 814.8}}})
	if v := got[start]; math.Abs(v-212.875) > 0.001 {
		t.Fatalf("planned = %v kWh, want 212.875", v)
	}
}

func TestPlanHoursClipsToTheInverter(t *testing.T) {
	start := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	got := PlanHours(pdeStation(), [][]HourInput{{{Start: start, TempC: 5, GtiWm2: 1100}}})
	if v := got[start]; v != 250 {
		t.Fatalf("planned = %v kWh, want the 250 kW inverter limit", v)
	}
}

// Clipping each plane on its own would let two planes that each fit
// the inverter add up past it.
func TestPlanHoursClipsOncePerStation(t *testing.T) {
	st := Station{
		Source:       SourceModel,
		PacLimitKw:   100,
		GammaPctPerC: 0,
		Orientations: []Orientation{
			{Idx: 1, TiltDeg: 30, ModulesCount: 100, ModulePowerW: 1000, Enabled: true},
			{Idx: 2, TiltDeg: 30, ModulesCount: 100, ModulePowerW: 1000, Enabled: true},
		},
	}
	start := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	hour := HourInput{Start: start, TempC: 25, GtiWm2: 800}
	got := PlanHours(st, [][]HourInput{{hour}, {hour}})
	if v := got[start]; v != 100 {
		t.Fatalf("planned = %v kWh, want one 100 kW clip for the station", v)
	}
}

func TestPlanHoursLeavesOutHoursAPlaneLacks(t *testing.T) {
	st := Station{
		Source:     SourceModel,
		PacLimitKw: 500,
		Orientations: []Orientation{
			{Idx: 1, ModulesCount: 100, ModulePowerW: 500, Enabled: true},
			{Idx: 2, ModulesCount: 100, ModulePowerW: 500, Enabled: true},
		},
	}
	a := time.Date(2026, 6, 21, 9, 0, 0, 0, time.UTC)
	b := a.Add(time.Hour)
	got := PlanHours(st, [][]HourInput{
		{{Start: a, GtiWm2: 500}, {Start: b, GtiWm2: 500}},
		{{Start: a, GtiWm2: 500}},
	})
	if _, ok := got[b]; ok {
		t.Fatal("an hour one plane has no weather for must not be counted as dark")
	}
	if _, ok := got[a]; !ok {
		t.Fatal("the hour both planes have must be planned")
	}
}

// A disabled plane takes no part: its weather is neither fetched nor
// added.
func TestPlanHoursIgnoresDisabledPlanes(t *testing.T) {
	st := pdeStation()
	st.Orientations = append(st.Orientations, Orientation{Idx: 2, ModulesCount: 100, ModulePowerW: 620, Enabled: false})
	start := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	got := PlanHours(st, [][]HourInput{{{Start: start, TempC: 24.9, GtiWm2: 814.8}}})
	if v := got[start]; math.Abs(v-212.875) > 0.001 {
		t.Fatalf("planned = %v kWh, want the enabled plane's 212.875", v)
	}
}

func hoursOf(from, to time.Time, kwh float64) map[time.Time]float64 {
	out := map[time.Time]float64{}
	for t := from; t.Before(to); t = t.Add(time.Hour) {
		out[t] = kwh
	}
	return out
}

func TestGroupDaysKeepsOnlyCompleteDays(t *testing.T) {
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("no tzdata")
	}
	day := time.Date(2026, 11, 3, 0, 0, 0, 0, kyiv)
	hours := hoursOf(day.UTC(), day.AddDate(0, 0, 1).UTC(), 10)
	// Half of the next day only.
	for k, v := range hoursOf(day.AddDate(0, 0, 1).UTC(), day.AddDate(0, 0, 1).Add(12*time.Hour).UTC(), 10) {
		hours[k] = v
	}
	days := GroupDays(hours, kyiv)
	if len(days) != 1 || days[0].Day != "2026-11-03" {
		t.Fatalf("days = %+v, want only the complete 2026-11-03", days)
	}
	if days[0].Total != 240 || len(days[0].Hours) != 24 {
		t.Fatalf("total = %v over %d hours, want 240 over 24", days[0].Total, len(days[0].Hours))
	}
}

// The autumn DST day has 25 hours and the spring one 23: both are
// complete days, not gaps.
func TestGroupDaysHandlesDSTDays(t *testing.T) {
	kyiv, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("no tzdata")
	}
	for _, day := range []time.Time{
		time.Date(2026, 10, 25, 0, 0, 0, 0, kyiv),
		time.Date(2026, 3, 29, 0, 0, 0, 0, kyiv),
	} {
		hours := hoursOf(day.UTC(), day.AddDate(0, 0, 1).UTC(), 1)
		days := GroupDays(hours, kyiv)
		if len(days) != 1 || days[0].Day != day.Format("2006-01-02") {
			t.Fatalf("%s: days = %+v, want the one DST day", day.Format("2006-01-02"), days)
		}
	}
}
