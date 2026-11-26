package api

import (
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/dispatch"
	"github.com/nesh/sestelemetry/internal/offgrid"
)

// The charge is due at the end of the hour before the outage's hour;
// an outage under way or starting this hour has nothing to charge for.
func TestOffgridFloors(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Kyiv")
	start := time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC)
	env := &dispatchEnv{siteID: "ze", start: start, loc: loc}
	d := &offgridData{
		params: offgrid.Params{CapacityKwh: 1720, FloorPct: 20, MaxPct: 90, ErrorMarginPct: 20},
		outages: []offgrid.Outage{
			{Start: start.Add(3 * time.Hour), End: start.Add(5 * time.Hour)},
			{Start: start.Add(-time.Hour), End: start.Add(time.Hour)},
			{Start: start.Add(30 * time.Minute), End: start.Add(2 * time.Hour)},
		},
		avg: map[int]float64{},
	}
	m := dispatch.Model{Loads: make([]*float64, dispatchFutureHours)}
	for i := range m.Loads {
		v := 300.0
		m.Loads[i] = &v
	}
	floors := d.floors(env, m)
	if len(floors) != 1 || floors[2] != 61.9 {
		t.Fatalf("floors %v, want {2: 61.9}", floors)
	}
	// Without the energy manager's plan the 7-day average stands in.
	d.avg = map[int]float64{13: 100, 14: 100}
	m.Loads = make([]*float64, dispatchFutureHours)
	r := d.reserves(env, m)[0]
	if r.Source != offgrid.SourceAvg7d || r.NeedKwh == nil || *r.NeedKwh != 240 {
		t.Fatalf("reserve from the average: %+v", r)
	}
}

// §7: after the grid comes back, can it fill the battery to the next
// outage's target in time?
func TestGridInTime(t *testing.T) {
	start := time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC)
	env := &dispatchEnv{start: start, site: dispatch.Site{CapacityKwh: 1720, ChargeKw: 864}}
	next := []offgrid.Reserve{{Start: start.Add(6 * time.Hour), TargetPct: 90}}
	if !gridInTime(env, next, start.Add(4*time.Hour), 30) {
		t.Fatal("two hours at 864 kW fill 60 % of 1 720 kWh")
	}
	if gridInTime(env, next, start.Add(5*time.Hour), 30) {
		t.Fatal("one hour does not")
	}
	if !gridInTime(env, nil, start, 30) {
		t.Fatal("no next outage: no hurry")
	}
}

func TestOffgridSettingsValidate(t *testing.T) {
	if err := (&EdgeSiteSettings{}).validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	for name, s := range map[string]EdgeSiteSettings{
		"floor above the max":     {IslandSocFloorPct: 95},
		"min above the target":    {GeneratorMinKw: 500},
		"target above the rating": {GeneratorTargetKw: 800},
		"percent out of range":    {OutageErrorMarginPct: 120},
		"negative stop threshold": {ExtraStopThresholdUah: -1},
	} {
		if err := s.validate(); err == nil {
			t.Errorf("%s passed", name)
		}
	}
	o := (&EdgeSiteSettings{OutageReserveApply: true, GeneratorTargetKw: 500}).offgrid()
	if !o.Apply || o.GeneratorTargetKw != 500 || o.GeneratorMinKw != 220 || o.MaxPct != 90 {
		t.Fatalf("offgrid settings %+v", o)
	}
}
