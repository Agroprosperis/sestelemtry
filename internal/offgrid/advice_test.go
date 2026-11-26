package offgrid

import (
	"math"
	"strings"
	"testing"
	"time"
)

var zeAdvice = AdviceParams{
	CapacityKwh: 1720, FloorPct: 20, SwitchSocPct: 30, ChargeKw: 864,
	IslandMaxKw: 576, GenRatedKw: 734, GenMinKw: 220,
	ExtraStopThresholdUah: 1000, RechargeUahPerKwh: 5,
	Fuel: Fuel{Set: true, Method: "per_kwh", LPerKwh: 0.26, UahPerKwh: 14.3},
}

func flatHours(from time.Time, h int, load float64) []Hour {
	return HoursOf(from, from.Add(time.Duration(h)*time.Hour), func(time.Time) (float64, float64) { return load, 0 })
}

func variant(a Advice, id string) Variant {
	for _, v := range a.Variants {
		if v.ID == id {
			return v
		}
	}
	return Variant{}
}

// A short outage at a light load: the island is the cheapest.
func TestAdviseIslandForAShortOutage(t *testing.T) {
	a := Advise(flatHours(at(10, 0), 2, 200), 80, zeAdvice)
	if va := variant(a, VariantIsland); !va.Feasible || *va.CostUah != 2000 || va.EndSoc != 56.7 {
		t.Fatalf("A %+v", va)
	}
	// 200 kW is below the generator's 220: it charges the battery 20 kW.
	vb := variant(a, VariantGenerator)
	if !vb.Feasible || *vb.CostUah != 6092 || vb.FuelL != 114.4 {
		t.Fatalf("B %+v", vb)
	}
	if vc := variant(a, VariantIslandToGen); vc.Feasible || !strings.Contains(vc.Why, "вистачає") {
		t.Fatalf("C %+v", vc)
	}
	if a.Recommended != VariantIsland {
		t.Fatalf("advice %s: %s", a.Recommended, a.Why)
	}
}

// A long one: the island hands over at 30 %; C beats B by more than the
// threshold, so the extra stop is worth it — not with a higher one.
func TestAdviseIslandThenGenerator(t *testing.T) {
	hours := flatHours(at(8, 0), 8, 300)
	a := Advise(hours, 60, zeAdvice)
	if va := variant(a, VariantIsland); va.Feasible {
		t.Fatalf("A %+v", va)
	}
	vc := variant(a, VariantIslandToGen)
	if !vc.Feasible || vc.SwitchAt == nil || !vc.SwitchAt.Equal(at(9, 43)) || vc.Stops != 3 {
		t.Fatalf("C %+v (switch %v)", vc, vc.SwitchAt)
	}
	vb := variant(a, VariantGenerator)
	if a.Recommended != VariantIslandToGen || *vb.CostUah-*vc.CostUah < 1000 {
		t.Fatalf("advice %s (%v vs %v)", a.Recommended, *vb.CostUah, *vc.CostUah)
	}
	p := zeAdvice
	p.ExtraStopThresholdUah = 10000
	if a := Advise(hours, 60, p); a.Recommended != VariantGenerator {
		t.Fatalf("high threshold: %s", a.Recommended)
	}
}

func TestAdviseLimits(t *testing.T) {
	a := Advise(flatHours(at(10, 0), 1, 700), 80, zeAdvice)
	if va := variant(a, VariantIsland); va.Feasible || !strings.Contains(va.Why, "острова") {
		t.Fatalf("700 kW island: %+v", va)
	}
	if a.Recommended != VariantGenerator {
		t.Fatalf("700 kW: %s", a.Recommended)
	}
	a = Advise(flatHours(at(10, 0), 1, 900), 80, zeAdvice)
	if a.LimitLoadKw == nil || *a.LimitLoadKw != 734 || variant(a, VariantGenerator).Feasible || a.Recommended != "" {
		t.Fatalf("900 kW: %+v", a)
	}
	p := zeAdvice
	p.Fuel = Fuel{}
	a = Advise(flatHours(at(10, 0), 1, 300), 80, p)
	if vb := variant(a, VariantGenerator); vb.CostUah != nil || !strings.Contains(strings.Join(vb.Notes, ";"), "пального") {
		t.Fatalf("no fuel price: %+v", vb)
	}
}

func TestRechargePrice(t *testing.T) {
	got := RechargePrice([]float64{9, 3, 5, 4}, 1000, 864, 0.9, 0.6)
	if math.Abs(got-(3.5/0.9+0.6)) > 1e-9 {
		t.Fatalf("recharge %v", got)
	}
	if RechargePrice(nil, 100, 864, 0.9, 0.6) != 0 {
		t.Fatal("no prices")
	}
}

func TestGenCharge(t *testing.T) {
	if h := GenCharge(150, zeAdvice, 480, 55, 1.2, 0.26, true); h.TargetKw != 220 || !strings.Contains(h.Why, "обов'язковий") {
		t.Fatalf("below the minimum: %+v", h)
	}
	if h := GenCharge(300, zeAdvice, 480, 55, 1.2, 0.26, true); h.TargetKw != 220 || h.GenChargeUahPerKwh != 17.16 {
		t.Fatalf("dearer than the grid: %+v", h)
	}
	if h := GenCharge(300, zeAdvice, 480, 55, 1.2, 0.26, false); h.TargetKw != 480 {
		t.Fatalf("no time to recharge from the grid: %+v", h)
	}
	cheap := zeAdvice
	cheap.RechargeUahPerKwh = 20
	if h := GenCharge(300, cheap, 480, 55, 1.2, 0.26, true); h.TargetKw != 480 {
		t.Fatalf("cheaper than the grid: %+v", h)
	}
}

func TestAvailableNow(t *testing.T) {
	now := at(10, 0)
	a := AvailableNow(now, 60, 800, 50, 300, flatHours(now, 4, 300), zeAdvice)
	if a.IslandKw != 626 || a.GeneratorKw != 1534 {
		t.Fatalf("power %+v", a)
	}
	// 688 kWh over 250 kW net: 165 min; the generator by 12:31.
	if a.AutonomyNowMin == nil || *a.AutonomyNowMin != 165 || !a.StartGeneratorBy.Equal(at(12, 31)) {
		t.Fatalf("autonomy %+v", a)
	}
	if a.AutonomyPlanMin == nil || *a.AutonomyPlanMin != 138 {
		t.Fatalf("plan autonomy %v", a.AutonomyPlanMin)
	}
}
