package energyflow

import (
	"math"
	"testing"
	"time"
)

// groupSite builds 12 minutes of one site of a group: SmartLogger
// counters at every minute and Encombi readings every 5 s, the one
// generator meter giving 36 kWh. chg is the battery's charge a minute,
// imp the grid's; gridBits / genBits are the site's own breakers, the
// generator's power is the group's.
func groupSite(chg, imp, gridBits, genBits float64) GroupSite {
	var rows []RawSample
	c, i := 300.0, 1000.0
	for m := 0; m <= 12; m++ {
		at := supplyT0.Add(time.Duration(m) * time.Minute)
		rows = append(rows, RawSample{Time: at, Role: RoleSingle, Values: map[string]float64{
			SrcAccumulatedPVYieldKwh:   500,
			SrcAccumulatedPurchasedKwh: i,
			SrcAccumulatedSoldKwh:      200,
			SrcTotalEssChargedKwh:      c,
			SrcTotalEssDischargedKwh:   400,
		}})
		c += chg
		i += imp
		for s := 5; s <= 60; s += 5 {
			rows = append(rows, genRow(at.Add(time.Duration(s-60)*time.Second), gridBits, genBits, 360, 232))
		}
	}
	rows = append(rows, meterRow(supplyT0, 77700, 1), meterRow(supplyT0.Add(12*time.Minute), 77736, 1))
	return GroupSite{
		Rows:     rows,
		Timeline: BuildGenTimeline(rows, supplyT0, supplyT0.Add(12*time.Minute), time.Minute, time.Time{}),
		Opts:     Options{AllocationWindowSeconds: 60},
	}
}

func walkGroup(sites []GroupSite) (sum GenResult, modes map[SupplyMode]int, partial int) {
	modes = map[SupplyMode]int{}
	IterateGroupWindows(sites, sites[0].Timeline, func(w GroupWindow) {
		sum.add(w.Result)
		modes[w.Mode]++
		if w.Partial {
			partial++
		}
	})
	return sum, modes, partial
}

// Дубовʼязівський: both sites without the grid on the one generator,
// both batteries charging. The generator counts once, every charge is
// the generator's, and the time on the generator is the hour's once.
func TestGroupBothOnGenerator(t *testing.T) {
	sum, modes, partial := walkGroup([]GroupSite{groupSite(1, 0, 0, 1), groupSite(2, 0, 0, 1)})
	if math.Abs(sum.GenKwh-36) > 1e-6 {
		t.Fatalf("generator energy %g, want the one meter's 36 kWh", sum.GenKwh)
	}
	if math.Abs(sum.GenToESSKwh-36) > 1e-6 || sum.GridToESSKwh != 0 {
		t.Fatalf("gen_to_ess %g, grid_to_ess %g: both batteries charge from the generator", sum.GenToESSKwh, sum.GridToESSKwh)
	}
	if modes[SupplyGenerator] != 12 || partial != 0 {
		t.Fatalf("modes %v, partial %d", modes, partial)
	}
}

// One site without the grid on the generator, the other still on the
// grid (its controller sees the generator running: transition). The
// generator feeds only the first; the second keeps its own grid split.
func TestGroupOneOffTheGrid(t *testing.T) {
	sum, modes, partial := walkGroup([]GroupSite{groupSite(1, 0, 0, 1), groupSite(2, 2, 1, 0)})
	if math.Abs(sum.GenKwh-36) > 1e-6 || math.Abs(sum.GenToESSKwh-12) > 1e-6 {
		t.Fatalf("generator %g kWh, gen_to_ess %g: the generator charges only the site without the grid", sum.GenKwh, sum.GenToESSKwh)
	}
	if math.Abs(sum.GridToESSKwh-24) > 1e-6 {
		t.Fatalf("grid_to_ess %g, want the grid site's 24 kWh", sum.GridToESSKwh)
	}
	if modes[SupplyGenerator] != 12 || partial != 12 {
		t.Fatalf("modes %v, partial %d", modes, partial)
	}
}

// Both on the grid: nothing of the generator, the plain splits summed.
func TestGroupOnTheGrid(t *testing.T) {
	a, b := groupSite(1, 1, 1, 0), groupSite(2, 2, 1, 0)
	for _, s := range []*GroupSite{&a, &b} {
		for i := range s.Rows {
			if s.Rows[i].Role == RoleGen {
				s.Rows[i].Values[SrcActiveGenPowerKw] = 0
			}
		}
		s.Timeline = BuildGenTimeline(s.Rows, supplyT0, supplyT0.Add(12*time.Minute), time.Minute, time.Time{})
	}
	sum, modes, _ := walkGroup([]GroupSite{a, b})
	if sum.GenToESSKwh != 0 || sum.GenToLoadKwh != 0 || math.Abs(sum.GridToESSKwh-36) > 1e-6 {
		t.Fatalf("result %+v", sum)
	}
	if modes[SupplyGrid] != 12 {
		t.Fatalf("modes %v", modes)
	}
}
