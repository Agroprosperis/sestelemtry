package economics

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"
)

// planParams is a 100 kWh / 50 kW battery with realistic friction, so a
// price spread has to be worth more than the round trip before the
// optimizer will trade on it.
var planParams = optimumParams{
	capacityKwh:          100,
	degradationUahPerKwh: 0.6,
	maxChargeKwh:         50,
	maxDischargeKwh:      50,
	socMinKwh:            0,
	socMaxKwh:            100,
	rte:                  0.9,
}

// arbitrageDay is a synthetic day with a cheap night (hours 1-3) and an
// expensive evening (hours 19-21): the textbook case the optimizer should
// answer with "charge at night, discharge into the evening load".
func arbitrageDay(loc *time.Location, priced bool) []HourlyRecord {
	dayStart := time.Date(2026, 4, 1, 0, 0, 0, 0, loc)
	out := make([]HourlyRecord, 24)
	for h := 0; h < 24; h++ {
		rec := HourlyRecord{
			HourStart:  dayStart.Add(time.Duration(h) * time.Hour),
			GridToLoad: 200,
			GridImport: 200,
		}
		price := 5.0
		switch {
		case h >= 1 && h <= 3:
			price = 1
		case h >= 19 && h <= 21:
			price = 20
		}
		if priced {
			rdn := price * 1000
			rec.Rdn = &rdn
			rec.ImportPrice = price
			rec.ExportPrice = price
		}
		if h == 0 {
			rec.EssRemainingKwhStart = floatPtr(0)
		}
		out[h] = rec
	}
	return out
}

func planFor(t *testing.T, hourly []HourlyRecord, loc *time.Location) DayPlan {
	t.Helper()
	opts := buildDayOpts(hourly, loc, nil)
	do := opts["2026-04-01"]
	if do == nil {
		t.Fatal("buildDayOpts produced no day")
	}
	return buildDayPlan("2026-04-01", do, planParams, planParams.capacityKwh, 50, 0, loc)
}

// TestBuildDayPlanArbitrage: the recommendation charges in the cheap
// quartile, discharges in the expensive one, and never leaves the SOC
// window — the three properties the chart's two lines assert visually.
func TestBuildDayPlanArbitrage(t *testing.T) {
	loc := time.UTC
	hourly := arbitrageDay(loc, true)
	plan := planFor(t, hourly, loc)

	if !plan.Available {
		t.Fatal("plan should be available for a fully-priced day")
	}
	if len(plan.Hours) != 24 {
		t.Fatalf("got %d hours, want 24", len(plan.Hours))
	}

	var cheapCharge, peakDischarge float64
	for _, h := range plan.Hours {
		kw := h.RecommendedEssKw
		if h.Hour >= 1 && h.Hour <= 3 && kw < 0 {
			cheapCharge += -kw
		}
		if h.Hour >= 19 && h.Hour <= 21 && kw > 0 {
			peakDischarge += kw
		}
		if h.SocPct < 0 || h.SocPct > 100 {
			t.Fatalf("hour %d: SOC %.1f%% outside 0..100", h.Hour, h.SocPct)
		}
	}
	if cheapCharge <= 0 {
		t.Error("expected the plan to charge during the cheap night hours")
	}
	if peakDischarge <= 0 {
		t.Error("expected the plan to discharge during the expensive evening hours")
	}

	// The direction is what must never invert: buying into the evening
	// peak or selling through the cheap night would be a straight loss,
	// however the optimizer fills the mid-priced hours in between.
	for _, h := range plan.Hours {
		if h.Hour >= 19 && h.Hour <= 21 && h.RecommendedEssKw < -planIdleKwh {
			t.Errorf("hour %d: charging %.2f kW into the evening peak", h.Hour, -h.RecommendedEssKw)
		}
		if h.Hour >= 1 && h.Hour <= 3 && h.RecommendedEssKw > planIdleKwh {
			t.Errorf("hour %d: discharging %.2f kW through the cheap night", h.Hour, h.RecommendedEssKw)
		}
	}
}

// TestBuildDayPlanEffectMatchesOptimizeDay: the per-hour effects the
// tooltip shows must add up to the same optimum the monthly reserve is
// computed from, or the daily and monthly pages would disagree.
func TestBuildDayPlanEffectMatchesOptimizeDay(t *testing.T) {
	loc := time.UTC
	hourly := arbitrageDay(loc, true)
	plan := planFor(t, hourly, loc)

	opts := buildDayOpts(hourly, loc, nil)
	want := optimizeDay(opts["2026-04-01"].hours[:], 0, planParams, modeFull)

	var sum float64
	for _, h := range plan.Hours {
		sum += h.EffectUah
	}
	if math.Abs(sum-want) > 1e-6 {
		t.Errorf("Σ hourly effect = %v, optimizeDay = %v", sum, want)
	}
	if math.Abs(plan.Totals.OptimumUah-want) > 1e-6 {
		t.Errorf("totals optimum = %v, optimizeDay = %v", plan.Totals.OptimumUah, want)
	}
}

// TestBuildDayPlanReasonsMatchActions guards the explainability contract:
// every hour carries an action, a reason code consistent with it, and
// operator-facing text.
func TestBuildDayPlanReasonsMatchActions(t *testing.T) {
	loc := time.UTC
	plan := planFor(t, arbitrageDay(loc, true), loc)

	for _, h := range plan.Hours {
		if h.ReasonText == "" {
			t.Errorf("hour %d: empty reason text", h.Hour)
		}
		switch h.Action {
		case "charge":
			if h.RecommendedEssKw >= 0 {
				t.Errorf("hour %d: action=charge but power %.2f kW is not negative", h.Hour, h.RecommendedEssKw)
			}
			if !strings.HasPrefix(h.ReasonCode, "CHARGE_") {
				t.Errorf("hour %d: action=charge with reason %q", h.Hour, h.ReasonCode)
			}
		case "discharge":
			if h.RecommendedEssKw <= 0 {
				t.Errorf("hour %d: action=discharge but power %.2f kW is not positive", h.Hour, h.RecommendedEssKw)
			}
			if !strings.HasPrefix(h.ReasonCode, "DISCHARGE_") {
				t.Errorf("hour %d: action=discharge with reason %q", h.Hour, h.ReasonCode)
			}
		case "hold":
			if math.Abs(h.RecommendedEssKw) > planIdleKwh {
				t.Errorf("hour %d: action=hold but power is %.2f kW", h.Hour, h.RecommendedEssKw)
			}
		default:
			t.Errorf("hour %d: unknown action %q", h.Hour, h.Action)
		}
	}

}

// TestBuildDayPlanHoldsForPeak: a battery that starts full and has no way
// to monetise its charge until the evening should be explained as waiting
// for that peak, not as "the price is too low to bother".
func TestBuildDayPlanHoldsForPeak(t *testing.T) {
	loc := time.UTC
	dayStart := time.Date(2026, 4, 1, 0, 0, 0, 0, loc)
	hourly := make([]HourlyRecord, 24)
	for h := 0; h < 24; h++ {
		rec := HourlyRecord{HourStart: dayStart.Add(time.Duration(h) * time.Hour)}
		rdn := 5000.0
		rec.Rdn = &rdn
		rec.ImportPrice = 5
		// No load to displace and no export value outside the peak, so
		// there is nothing the stored energy can be sold into yet.
		if h == 20 {
			rdn = 20000
			rec.ImportPrice = 20
			rec.GridToLoad = 200
			rec.GridImport = 200
		}
		if h == 0 {
			rec.EssRemainingKwhStart = floatPtr(100)
		}
		hourly[h] = rec
	}
	plan := planFor(t, hourly, loc)

	for _, h := range plan.Hours {
		if h.Hour >= 20 {
			continue
		}
		if h.Action != "hold" {
			t.Fatalf("hour %d: action %q, want hold", h.Hour, h.Action)
		}
		if h.ReasonCode != ReasonHoldForFuturePeak {
			t.Errorf("hour %d: reason %q, want %q", h.Hour, h.ReasonCode, ReasonHoldForFuturePeak)
		}
	}
	if plan.Hours[20].Action != "discharge" {
		t.Errorf("hour 20: action %q, want discharge", plan.Hours[20].Action)
	}
}

// TestBuildDayPlanWithoutPrices: with no РДН the optimizer has nothing to
// trade against, so it must recommend nothing and say why rather than
// invent a schedule.
func TestBuildDayPlanWithoutPrices(t *testing.T) {
	loc := time.UTC
	plan := planFor(t, arbitrageDay(loc, false), loc)

	for _, h := range plan.Hours {
		if math.Abs(h.RecommendedEssKw) > planIdleKwh {
			t.Errorf("hour %d: dispatch %.2f kW without a price", h.Hour, h.RecommendedEssKw)
		}
		if h.ReasonCode != ReasonNoPrice {
			t.Errorf("hour %d: reason %q, want %q", h.Hour, h.ReasonCode, ReasonNoPrice)
		}
	}
	if plan.Totals.OptimumUah != 0 {
		t.Errorf("optimum = %v, want 0 without prices", plan.Totals.OptimumUah)
	}
	found := false
	for _, w := range plan.Warnings {
		if w == WarnNoPrices {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v, want %s", plan.Warnings, WarnNoPrices)
	}
}

// TestBuildDayPlanNoData: a day the optimizer never saw is reported as
// unavailable, which is what makes the chart overlay degrade quietly.
func TestBuildDayPlanNoData(t *testing.T) {
	if plan := buildDayPlan("2026-04-01", nil, planParams, 100, 50, 0, time.UTC); plan.Available {
		t.Error("a nil day should not produce an available plan")
	}
}

// TestGetDayPlanEndToEnd exercises the service wiring: tz resolution,
// tariff lookup, the hourly load, and the plan returned to the handler.
func TestGetDayPlanEndToEnd(t *testing.T) {
	b, loc := newKyivBackend(t)
	tariffs := flatTariffs
	tariffs.EssCapacityKwh = 100
	tariffs.EssPowerLimitKw = 50
	tariffs.RoundtripEfficiency = 1
	b.schedule = Schedule{{EffectiveFrom: mustDate("1970-01-01"), Tariffs: tariffs}}
	b.hourly = arbitrageDay(loc, true)

	plan, err := NewService(b).GetDayPlan(context.Background(), "org1", "2026-04-01", "Europe/Kyiv")
	if err != nil {
		t.Fatalf("GetDayPlan: %v", err)
	}
	if !plan.Available {
		t.Fatal("expected an available plan")
	}
	if plan.OrganizationID != "org1" || plan.Date != "2026-04-01" || plan.Tz != "Europe/Kyiv" {
		t.Errorf("identity fields = %q/%q/%q", plan.OrganizationID, plan.Date, plan.Tz)
	}
	if plan.CapacityKwh != 100 || plan.PowerKw != 50 {
		t.Errorf("envelope = %v kWh / %v kW, want 100/50", plan.CapacityKwh, plan.PowerKw)
	}
	if plan.Totals.OptimumUah <= 0 {
		t.Errorf("optimum = %v, want a profitable day", plan.Totals.OptimumUah)
	}
	if plan.Totals.ReserveUah <= 0 {
		t.Errorf("reserve = %v, want the idle battery to leave the whole optimum on the table",
			plan.Totals.ReserveUah)
	}
}

// loadShiftDay is a synthetic elevator day: constant 20 kWh of base load
// every hour, heavy milling (180 kWh extra) in the expensive evening, an
// exported PV surplus at midday and a cheap night — exactly the day the
// schedule recommendation exists to fix.
func loadShiftDay(loc *time.Location) []HourlyRecord {
	dayStart := time.Date(2026, 4, 1, 0, 0, 0, 0, loc)
	out := make([]HourlyRecord, 24)
	for h := 0; h < 24; h++ {
		rec := HourlyRecord{HourStart: dayStart.Add(time.Duration(h) * time.Hour)}
		price := 5.0
		switch {
		case h >= 1 && h <= 3:
			price = 1
		case h >= 19 && h <= 21:
			price = 20
		}
		rdn := price * 1000
		rec.Rdn = &rdn
		rec.ImportPrice = price
		rec.ExportPrice = price * 0.8

		rec.GridToLoad = 20
		if h >= 19 && h <= 21 {
			rec.GridToLoad = 80 // 60 kWh/h of movable evening milling
		}
		if h >= 11 && h <= 13 {
			rec.PVToGrid = 100 // exported PV surplus nobody consumed
		}
		rec.GridImport = rec.GridToLoad
		out[h] = rec
	}
	return out
}

func recommendedFor(t *testing.T, hourly []HourlyRecord, loc *time.Location) [24]*float64 {
	t.Helper()
	opts := buildDayOpts(hourly, loc, nil)
	do := opts["2026-04-01"]
	if do == nil {
		t.Fatal("buildDayOpts produced no day")
	}
	return recommendLoad(do, 0)
}

// TestRecommendLoadShiftsIntoPvAndCheapHours locks in the three invariants
// the chart line asserts visually: daily energy is conserved, no hour
// exceeds the demonstrated peak, and the movable load lands in the
// PV-surplus and cheap hours instead of the expensive evening.
func TestRecommendLoadShiftsIntoPvAndCheapHours(t *testing.T) {
	loc := time.UTC
	hourly := loadShiftDay(loc)
	rec := recommendedFor(t, hourly, loc)

	var totalFact, totalRec float64
	for h, r := range hourly {
		totalFact += r.GridToLoad
		if rec[h] == nil {
			t.Fatalf("hour %d: no recommendation", h)
		}
		totalRec += *rec[h]
		if *rec[h] > 80+1e-6 {
			t.Errorf("hour %d: %.1f kWh exceeds the demonstrated 80 kWh peak", h, *rec[h])
		}
		if *rec[h] < 20-1e-6 {
			t.Errorf("hour %d: %.1f kWh drops below the 20 kWh base load", h, *rec[h])
		}
	}
	if math.Abs(totalRec-totalFact) > 1e-6 {
		t.Fatalf("energy not conserved: recommended %.3f vs actual %.3f", totalRec, totalFact)
	}

	// The evening milling must move out, and with the night import (1)
	// cheaper than the forgone PV export (4) the 180 kWh fills the three
	// night hours to the 80 kWh peak.
	for h := 19; h <= 21; h++ {
		if *rec[h] > 20+1e-6 {
			t.Errorf("hour %d: %.1f kWh still scheduled in the expensive evening", h, *rec[h])
		}
	}
	for h := 1; h <= 3; h++ {
		if *rec[h] < 80-1e-6 {
			t.Errorf("hour %d: %.1f kWh, want the full 80 kWh in the cheap night", h, *rec[h])
		}
	}
}

// TestRecommendLoadPrefersPvSurplus: without a cheap night, consuming the
// exported PV (cost = forgone export, 4) beats running on grid at the flat
// import price (5), so the milling moves under the solar peak.
func TestRecommendLoadPrefersPvSurplus(t *testing.T) {
	loc := time.UTC
	hourly := loadShiftDay(loc)
	for i := range hourly {
		if i >= 1 && i <= 3 {
			rdn := 5000.0
			hourly[i].Rdn = &rdn
			hourly[i].ImportPrice = 5
			hourly[i].ExportPrice = 4
		}
	}
	rec := recommendedFor(t, hourly, loc)

	for h := 11; h <= 13; h++ {
		if *rec[h] < 80-1e-6 {
			t.Errorf("hour %d: %.1f kWh, want the full 80 kWh under the PV surplus", h, *rec[h])
		}
	}
	for h := 19; h <= 21; h++ {
		if *rec[h] > 20+1e-6 {
			t.Errorf("hour %d: %.1f kWh still scheduled in the expensive evening", h, *rec[h])
		}
	}
}

// TestRecommendLoadWindowPeakUnlocksQuietDay: on a quiet day the day's
// own maximum is idle-level and the recommendation degenerates to a
// ripple. A demonstrated peak from the trailing window lets the same
// daily energy concentrate into the cheapest hours above the day's own
// maximum — while the total, the base, and the window ceiling still hold.
func TestRecommendLoadWindowPeakUnlocksQuietDay(t *testing.T) {
	loc := time.UTC
	hourly := loadShiftDay(loc)
	opts := buildDayOpts(hourly, loc, nil)
	do := opts["2026-04-01"]
	if do == nil {
		t.Fatal("buildDayOpts produced no day")
	}
	rec := recommendLoad(do, 300)

	var totalFact, totalRec, maxRec float64
	for h, r := range hourly {
		totalFact += r.GridToLoad
		if rec[h] == nil {
			t.Fatalf("hour %d: no recommendation", h)
		}
		totalRec += *rec[h]
		if *rec[h] > maxRec {
			maxRec = *rec[h]
		}
		if *rec[h] > 300+1e-6 {
			t.Errorf("hour %d: %.1f kWh exceeds the window ceiling", h, *rec[h])
		}
		if *rec[h] < 20-1e-6 {
			t.Errorf("hour %d: %.1f kWh drops below the base", h, *rec[h])
		}
	}
	if math.Abs(totalRec-totalFact) > 1e-6 {
		t.Fatalf("energy not conserved: recommended %.3f vs actual %.3f", totalRec, totalFact)
	}
	// The whole movable volume (180 kWh) now fits into the single
	// cheapest hour instead of being smeared across three at the day's
	// own 80 kWh maximum.
	if maxRec <= 80+1e-6 {
		t.Errorf("max recommended %.1f kWh — window ceiling did not unlock the day's own maximum", maxRec)
	}
}

// TestRecommendLoadKeepsSelfConsumedPv: milling that already runs on its
// own solar must stay under the sun. Its true cost is the forgone export
// (4), so a night import at 4.5 — cheaper than the day's import of 5 but
// dearer than the export — must NOT pull the load out of the solar hours.
func TestRecommendLoadKeepsSelfConsumedPv(t *testing.T) {
	loc := time.UTC
	dayStart := time.Date(2026, 4, 1, 0, 0, 0, 0, loc)
	hourly := make([]HourlyRecord, 24)
	for h := 0; h < 24; h++ {
		rec := HourlyRecord{HourStart: dayStart.Add(time.Duration(h) * time.Hour)}
		price := 5.0
		if h >= 1 && h <= 3 {
			price = 4.5
		}
		rdn := price * 1000
		rec.Rdn = &rdn
		rec.ImportPrice = price
		rec.ExportPrice = price * 0.8

		if h >= 11 && h <= 13 {
			// The whole 80 kWh — base and milling — is self-consumed PV;
			// nothing is exported.
			rec.PVToLoad = 80
		} else {
			rec.GridToLoad = 20
			rec.GridImport = 20
		}
		hourly[h] = rec
	}
	rec := recommendedFor(t, hourly, loc)

	for h := 11; h <= 13; h++ {
		if *rec[h] < 80-1e-6 {
			t.Errorf("hour %d: %.1f kWh — milling pulled off its own solar", h, *rec[h])
		}
	}
	for h := 1; h <= 3; h++ {
		if *rec[h] > 20+1e-6 {
			t.Errorf("hour %d: %.1f kWh scheduled at night although the sun was cheaper", h, *rec[h])
		}
	}
}

// TestRecommendLoadWithoutPricesEqualsFact: no РДН anywhere → nothing to
// optimise against, so the recommendation must be the factual profile,
// not an invented schedule.
func TestRecommendLoadWithoutPricesEqualsFact(t *testing.T) {
	loc := time.UTC
	hourly := loadShiftDay(loc)
	for i := range hourly {
		hourly[i].Rdn = nil
	}
	rec := recommendedFor(t, hourly, loc)
	for h, r := range hourly {
		if rec[h] == nil || math.Abs(*rec[h]-r.GridToLoad) > 1e-6 {
			t.Errorf("hour %d: recommended %v, want the factual %.1f", h, rec[h], r.GridToLoad)
		}
	}
}

// TestRecommendLoadFlatDay: with nothing movable (base == peak) the
// recommendation is the fact — there is no headroom to shift into.
func TestRecommendLoadFlatDay(t *testing.T) {
	loc := time.UTC
	hourly := loadShiftDay(loc)
	for i := range hourly {
		hourly[i].GridToLoad = 50
	}
	rec := recommendedFor(t, hourly, loc)
	for h := range hourly {
		if rec[h] == nil || math.Abs(*rec[h]-50) > 1e-6 {
			t.Errorf("hour %d: recommended %v, want 50", h, rec[h])
		}
	}
}

// TestRecommendLoadPartialPrices: hours without a РДН price must not be
// scheduled above base, and the energy they can't take spills into the
// priced hours while the daily total still balances.
func TestRecommendLoadPartialPrices(t *testing.T) {
	loc := time.UTC
	hourly := loadShiftDay(loc)
	// Strip prices from the night — the cheapest hours vanish from the
	// eligible set.
	for h := 0; h <= 6; h++ {
		hourly[h].Rdn = nil
	}
	rec := recommendedFor(t, hourly, loc)

	var totalFact, totalRec float64
	for h, r := range hourly {
		totalFact += r.GridToLoad
		if rec[h] == nil {
			t.Fatalf("hour %d: no recommendation", h)
		}
		totalRec += *rec[h]
		if h <= 6 && *rec[h] > 20+1e-6 {
			t.Errorf("hour %d: %.1f kWh of extra load scheduled without a price", h, *rec[h])
		}
	}
	if math.Abs(totalRec-totalFact) > 1e-6 {
		t.Fatalf("energy not conserved: recommended %.3f vs actual %.3f", totalRec, totalFact)
	}
}

// TestGetDayPlanCarriesRecommendedLoad: the service wires the schedule
// into the same payload as the УЗЕ plan.
func TestGetDayPlanCarriesRecommendedLoad(t *testing.T) {
	b, loc := newKyivBackend(t)
	tariffs := flatTariffs
	tariffs.EssCapacityKwh = 100
	tariffs.EssPowerLimitKw = 50
	b.schedule = Schedule{{EffectiveFrom: mustDate("1970-01-01"), Tariffs: tariffs}}
	b.hourly = loadShiftDay(loc)

	plan, err := NewService(b).GetDayPlan(context.Background(), "org1", "2026-04-01", "Europe/Kyiv")
	if err != nil {
		t.Fatalf("GetDayPlan: %v", err)
	}
	if !plan.Available {
		t.Fatal("expected an available plan")
	}
	var sum float64
	for _, h := range plan.Hours {
		if h.RecommendedLoadKw == nil {
			t.Fatalf("hour %d: recommended load missing", h.Hour)
		}
		sum += *h.RecommendedLoadKw
	}
	if math.Abs(sum-(21*20+3*80)) > 1e-6 {
		t.Errorf("Σ recommended = %.1f, want the day's actual load", sum)
	}
}

// TestGetDayPlanCeilingFromTrailingWindow: the service derives the load
// ceiling from the trailing window, so a milling hour a few days earlier
// lets a quiet day's recommendation exceed that day's own maximum.
func TestGetDayPlanCeilingFromTrailingWindow(t *testing.T) {
	b, loc := newKyivBackend(t)
	tariffs := flatTariffs
	tariffs.EssCapacityKwh = 100
	tariffs.EssPowerLimitKw = 50
	b.schedule = Schedule{{EffectiveFrom: mustDate("1970-01-01"), Tariffs: tariffs}}

	hourly := loadShiftDay(loc)
	rdn := 5000.0
	milling := HourlyRecord{
		HourStart:   time.Date(2026, 3, 27, 12, 0, 0, 0, loc),
		Rdn:         &rdn,
		ImportPrice: 5,
		ExportPrice: 4,
		GridToLoad:  250,
		GridImport:  250,
	}
	b.hourly = append([]HourlyRecord{milling}, hourly...)

	plan, err := NewService(b).GetDayPlan(context.Background(), "org1", "2026-04-01", "Europe/Kyiv")
	if err != nil {
		t.Fatalf("GetDayPlan: %v", err)
	}
	if !plan.Available {
		t.Fatal("expected an available plan")
	}
	var sum, maxRec float64
	for _, h := range plan.Hours {
		if h.RecommendedLoadKw == nil {
			continue
		}
		sum += *h.RecommendedLoadKw
		if *h.RecommendedLoadKw > maxRec {
			maxRec = *h.RecommendedLoadKw
		}
	}
	if maxRec <= 80+1e-6 {
		t.Errorf("max recommended %.1f kWh — the window's 250 kWh milling hour did not raise the ceiling", maxRec)
	}
	if maxRec > 250+1e-6 {
		t.Errorf("max recommended %.1f kWh exceeds the demonstrated 250 kWh", maxRec)
	}
	if want := 21*20.0 + 3*80.0; math.Abs(sum-want) > 1e-6 {
		t.Errorf("Σ recommended = %.1f, want the plan day's own energy %.1f", sum, want)
	}
}

func TestGetDayPlanRejectsBadDate(t *testing.T) {
	b, _ := newKyivBackend(t)
	if _, err := NewService(b).GetDayPlan(context.Background(), "org1", "01-04-2026", "Europe/Kyiv"); err == nil {
		t.Error("expected an error for a non-ISO date")
	}
}
