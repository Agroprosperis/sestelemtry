package offgrid

import (
	"testing"
)

func TestRecordsJoinTheScheduleWithTheFact(t *testing.T) {
	soc, plan := 70.0, 250.0
	hours := []FactHour{
		{TS: at(9, 0)},
		{TS: at(10, 0), MinIsland: 30, LoadKwh: 120, EssKwh: 120, SocStartPct: &soc, PlanLoadKw: &plan},
		{TS: at(11, 0), MinIsland: 20, MinGenerator: 40, GenKwh: 180, FuelL: 47, FuelUah: 2600, LoadKwh: 280, EssKwh: 80, PlanLoadKw: &plan},
		{TS: at(12, 0), MinGenerator: 15, GenKwh: 40, FuelL: 10, FuelUah: 580, LoadKwh: 70, PlanLoadKw: &plan},
		{TS: at(13, 0)},
		{TS: at(16, 0), MinBlackout: 10, LoadKwh: 0},
	}
	sched := []Outage{
		{Start: at(10, 30), End: at(12, 30), Kind: "schedule"},
		{Start: at(20, 0), End: at(22, 0), Kind: "schedule"},
	}
	recs, sum := Records(sched, hours, zeAdvice, 20)
	if len(recs) != 3 {
		t.Fatalf("records %+v", recs)
	}
	r := recs[0]
	if !r.Happened || !r.Start.Equal(at(10, 30)) || !r.End.Equal(at(12, 15)) || r.Kind != "schedule" {
		t.Fatalf("matched: %+v", r)
	}
	if r.MinutesIsland != 50 || r.MinutesGenerator != 55 || r.Stops != 3 || len(r.Sequence) != 2 || r.Sequence[0] != "острів" {
		t.Fatalf("modes: %+v", r)
	}
	// Plan 250 kW × 1.75 h = 437.5 kWh against 470 measured: +7.4 %.
	if r.LoadPlanKwh == nil || *r.LoadPlanKwh != 437.5 || r.LoadFactKwh != 470 || *r.DeviationPct != 7.4 {
		t.Fatalf("load: %+v", r)
	}
	// Fuel 3 180 + 200 kWh from the battery × 5.
	if r.FuelUah != 3180 || r.CostFactUah != 4180 || r.SocEndPct == nil || *r.SocEndPct != 58.4 {
		t.Fatalf("cost: %+v", r)
	}
	if len(r.Variants) != 3 || r.Advised == "" {
		t.Fatalf("variants: %+v", r)
	}
	// Hour by hour: 270 kW at 11:00, 160 kW at 12:00 — its 15 minutes
	// were below 220.
	if r.GenBelowMinMin != 15 {
		t.Fatalf("below the minimum: %d", r.GenBelowMinMin)
	}
	if recs[1].Kind != "unscheduled" || !recs[1].Happened || recs[1].MinutesBlackout != 10 {
		t.Fatalf("unscheduled: %+v", recs[1])
	}
	if recs[2].Happened || recs[2].ScheduledStart == nil {
		t.Fatalf("not happened: %+v", recs[2])
	}
	if sum.Outages != 2 || sum.Unscheduled != 1 || sum.FuelUah != 3180 || sum.WithPlan != 1 || *sum.UnderestimatePct != 7.4 {
		t.Fatalf("summary %+v", sum)
	}
}

func TestMarginFromFact(t *testing.T) {
	five := 5.0
	if m := MarginFromFact(Summary{WithPlan: 9, UnderestimatePct: &five}, 20); m != 20 {
		t.Fatalf("nine outages: %v", m)
	}
	if m := MarginFromFact(Summary{WithPlan: 10, UnderestimatePct: &five}, 20); m != 10 {
		t.Fatalf("ten, 5 %%: %v", m)
	}
	thirty := 30.0
	if m := MarginFromFact(Summary{WithPlan: 12, UnderestimatePct: &thirty}, 20); m != 30 {
		t.Fatalf("twelve, 30 %%: %v", m)
	}
}
