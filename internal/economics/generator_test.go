package economics

import (
	"context"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// genTariffs prices the fuel on top of flatTariffs (import = export = РДН,
// no VAT, no wear): c = 50 × 0.3 = 15 грн/кВт·год, h = 50 × 20 = 1000 грн/год.
func genTariffs(method string) Tariffs {
	t := flatTariffs
	t.GenFuelPriceUahPerL = 50
	t.GenFuelMethod = method
	t.GenFuelConsumptionLPerKwh = 0.3
	t.GenFuelConsumptionLPerH = 20
	return t
}

const (
	pvKey       = "accumulated_pv_energy_yield_kwh"
	purchaseKey = "accumulated_electricity_purchased_kwh"
)

// genDayFlows is 2026-10-20 at a site on the grid except three hours,
// priced at 2 грн/кВт·год all day:
//
//	02:00 generator 60 min, 150 kWh: load 100, charge 50 (the SmartLogger
//	      meter still claims 3 kWh of import);
//	10:00 grid: PV 50 (20 to load, 30 to the ESS), import 10;
//	12:00 generator 60 min, 70 kWh, with 30 kWh of PV: load 100;
//	18:00 generator 30 min (40 kWh) + island 30 min (ESS 20, PV 10);
//	20:00 grid: import 15, ESS 10 to load.
func genDayFlows(dayStart time.Time) ([]FlowRow, []Point) {
	hour := func(h int) time.Time { return dayStart.Add(time.Duration(h) * time.Hour) }
	mid := func(h int) time.Time { return hour(h).Add(30 * time.Minute) }
	flows := make([]FlowRow, 24)
	for h := range flows {
		flows[h] = FlowRow{From: hour(h)}
	}
	flows[2] = FlowRow{From: hour(2), EssCharged: 50, GridToEss: 50, Gen: &GenFlowRow{
		GenKwh: 150, Minutes: SupplyMinutes{Generator: 60},
		EssCharged: 50, GenToEss: 50, GenToLoad: 100, LoadOffgrid: 100,
	}}
	flows[10] = FlowRow{From: hour(10), EssCharged: 30, PVToEss: 30}
	flows[12] = FlowRow{From: hour(12), Gen: &GenFlowRow{
		GenKwh: 70, Minutes: SupplyMinutes{Generator: 60},
		GenToLoad: 70, LoadOffgrid: 100, PVToLoadOffgrid: 30,
	}}
	flows[18] = FlowRow{From: hour(18), EssDischarged: 20, EssToLoad: 20, Gen: &GenFlowRow{
		GenKwh: 40, Minutes: SupplyMinutes{Generator: 30, Island: 30},
		EssDischarged: 20, EssToLoad: 20, GenToLoad: 40,
		LoadOffgrid: 70, PVToLoadOffgrid: 10, EssToLoadOffgrid: 20,
		PVToLoadIsland: 10, EssToLoadIsland: 20,
	}}
	flows[20] = FlowRow{From: hour(20), EssDischarged: 10, EssToLoad: 10}
	deltas := []Point{
		{Time: mid(2), MetricKey: purchaseKey, Value: 3},
		{Time: mid(2), MetricKey: essChargedCounterKey, Value: 50},
		{Time: mid(10), MetricKey: pvKey, Value: 50},
		{Time: mid(10), MetricKey: purchaseKey, Value: 10},
		{Time: mid(10), MetricKey: essChargedCounterKey, Value: 30},
		{Time: mid(12), MetricKey: pvKey, Value: 30},
		{Time: mid(18), MetricKey: pvKey, Value: 10},
		{Time: mid(18), MetricKey: essDischargedCounterKey, Value: 20},
		{Time: mid(20), MetricKey: purchaseKey, Value: 15},
		{Time: mid(20), MetricKey: essDischargedCounterKey, Value: 10},
	}
	return flows, deltas
}

func genDayInput(t *testing.T, tariffs Tariffs, accounting bool) DayInput {
	t.Helper()
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	dayStart := time.Date(2026, 10, 20, 0, 0, 0, 0, loc)
	flows, deltas := genDayFlows(dayStart)
	return DayInput{
		DayStart:      dayStart,
		Tariffs:       tariffs,
		TodayFlows:    flows,
		DeltaPoints:   deltas,
		DamToday:      fullDayDAM("2026-10-20"),
		GenAccounting: accounting,
	}
}

// withoutGen is in with the generator data removed from the flow rows:
// the day as the code before the generator computed it.
func withoutGen(in DayInput) DayInput {
	flows := make([]FlowRow, len(in.TodayFlows))
	for i, f := range in.TodayFlows {
		f.Gen = nil
		flows[i] = f
	}
	in.TodayFlows = flows
	return in
}

// sameAsLegacy checks rows equal the legacy rows field for field once the
// informational generator fields are cleared.
func sameAsLegacy(t *testing.T, rows, legacy []*HourRow) {
	t.Helper()
	for h := range rows {
		if (rows[h] == nil) != (legacy[h] == nil) {
			t.Fatalf("hour %d: nil mismatch", h)
		}
		if rows[h] == nil {
			continue
		}
		r := *rows[h]
		r.GenKwh, r.GenEstimated, r.SupplyMinutes = 0, false, SupplyMinutes{}
		if !reflect.DeepEqual(&r, legacy[h]) {
			t.Fatalf("hour %d differs from legacy:\n got  %+v\n want %+v", h, r, *legacy[h])
		}
	}
	got, want := ComputeDailyTotals(rows), ComputeDailyTotals(legacy)
	got.GenTotals = GenTotals{}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("day totals differ from legacy:\n got  %+v\n want %+v", got, want)
	}
}

// Fuel not priced: the generator day keeps today's flows and money to the
// bit; only the generator's own energy and minutes are added.
func TestGenNotAccountedKeepsLegacy(t *testing.T) {
	in := genDayInput(t, flatTariffs, false)
	rows, recon := AssembleDay(in)
	legacy, legacyRecon := AssembleDay(withoutGen(in))
	sameAsLegacy(t, rows, legacy)
	if !reflect.DeepEqual(recon, legacyRecon) {
		t.Fatalf("recon = %+v, want legacy %+v", recon, legacyRecon)
	}
	tot := ComputeDailyTotals(rows)
	near(t, "gen kWh", tot.GenKwh, 260)
	if tot.SupplyMinutes != (SupplyMinutes{Generator: 150, Island: 30}) {
		t.Fatalf("minutes = %+v", tot.SupplyMinutes)
	}
	if tot.GenToLoad != 0 || tot.GenFuelCost != 0 || tot.ExpenseGenCharge != 0 {
		t.Fatalf("money without accounting: %+v", tot.GenTotals)
	}
}

// Accounting on, but the generator never ran and the grid never dropped:
// the day is the legacy one, transition and unknown minutes included.
func TestGenAccountedGridDayKeepsLegacy(t *testing.T) {
	in := genDayInput(t, genTariffs(GenFuelPerKwh), true)
	for h := range in.TodayFlows {
		in.TodayFlows[h].Gen = nil
	}
	in.TodayFlows[5].Gen = &GenFlowRow{Minutes: SupplyMinutes{Transition: 2, Unknown: 3}}
	in.TodayFlows[6].Gen = &GenFlowRow{}
	rows, _ := AssembleDay(in)
	legacy, _ := AssembleDay(withoutGen(in))
	sameAsLegacy(t, rows, legacy)
}

func TestGenPerKwhDay(t *testing.T) {
	rows, recon := AssembleDay(genDayInput(t, genTariffs(GenFuelPerKwh), true))

	night := rows[2]
	if !night.Flow.Gen.Accounted {
		t.Fatal("02:00 should be counted with the generator")
	}
	near(t, "night import (meter claim dropped)", night.Flow.GridImport, 0)
	near(t, "night grid_to_ess", night.Flow.GridToEss, 0)
	near(t, "night gen_to_ess", night.Flow.Gen.GenToEss, 50)
	near(t, "night gen_to_load", night.Flow.Gen.GenToLoad, 100)
	near(t, "night load", night.Econ.Load, 100)
	near(t, "night fuel", night.Econ.GenFuelCostUah, 2250)
	near(t, "night litres", night.Econ.GenFuelL, 45)
	near(t, "night baseline", night.Econ.BaselineCost, 1500)
	near(t, "night effect", night.Econ.Effect, -750)
	near(t, "night ess_net", night.Econ.EssNet, -750)
	near(t, "night: effect − ess_net (solar effect)", night.Econ.Effect-night.Econ.EssNet, 0)
	near(t, "night gen charge cost", night.Econ.GenChargeCostUah, 750)

	noon := rows[12]
	near(t, "noon load", noon.Econ.Load, 100)
	near(t, "noon fuel", noon.Econ.GenFuelCostUah, 1050)
	near(t, "noon effect = PV off grid × c", noon.Econ.Effect, 450)
	near(t, "noon PV off-grid value", noon.Econ.PvOffgridValueUah, 450)

	evening := rows[18]
	near(t, "evening load", evening.Econ.Load, 70)
	near(t, "evening effect", evening.Econ.Effect, 450)
	near(t, "evening ESS off-grid value", evening.Econ.EssOffgridValueUah, 300)
	near(t, "evening ess_net", evening.Econ.EssNet, 300)
	if evening.EssRealizedProfitUah == nil || evening.EssWithdrawnCostUah == nil {
		t.Fatal("evening cost basis missing")
	}
	near(t, "evening discharge revenue = V_ess_off", *evening.EssRealizedProfitUah+*evening.EssWithdrawnCostUah, 300)

	if rows[10].Flow.Gen.Accounted || rows[20].Flow.Gen.Accounted {
		t.Fatal("grid hours must stay legacy")
	}
	if !slices.Contains(recon.Flags, "offgrid_import:3") {
		t.Fatalf("flags = %v, want offgrid_import:3", recon.Flags)
	}

	tot := ComputeDailyTotals(rows)
	near(t, "day effect", tot.Effect, -750+40+450+450+20)
	near(t, "EBITDA = effect + wear", tot.Ebitda, tot.Effect+tot.EssDischarged*flatTariffs.DegradationUahPerKwh)
	near(t, "revenue PV self", tot.RevenuePvSelf, 40+450+150)
	near(t, "revenue ESS self", tot.RevenueEssSelf, 300+20)
	near(t, "expense gen charge", tot.ExpenseGenCharge, 750)
	near(t, "expense total", tot.ExpenseTotal, 750)
	near(t, "gen kWh", tot.GenKwh, 260)
	near(t, "gen to load", tot.GenToLoad, 210)
	near(t, "gen to ESS", tot.GenToEss, 50)
	near(t, "load off grid", tot.LoadOffgrid, 270)
	near(t, "fuel", tot.GenFuelCost, 3900)
	near(t, "litres", tot.GenFuelL, 78)
	if tot.GenFuelMethod != GenFuelPerKwh {
		t.Fatalf("method = %q", tot.GenFuelMethod)
	}
	if _, bad := balanceFlag(tot); bad {
		t.Fatal("a day with the generator in the balance must not read as negative")
	}
	if slices.Contains(recon.Flags, "load_rebalanced") {
		t.Fatalf("flags = %v: G belongs in the daily balance", recon.Flags)
	}
}

func TestGenPerHourDay(t *testing.T) {
	rows, _ := AssembleDay(genDayInput(t, genTariffs(GenFuelPerHour), true))

	night := rows[2]
	near(t, "night fuel = 1 h × h", night.Econ.GenFuelCostUah, 1000)
	near(t, "night litres", night.Econ.GenFuelL, 20)
	near(t, "night effect", night.Econ.Effect, 0)
	near(t, "night ess_net (c_ess = 0)", night.Econ.EssNet, 0)
	near(t, "night gen charge cost", night.Econ.GenChargeCostUah, 0)

	noon := rows[12]
	near(t, "noon: 60 min on the generator", noon.Econ.Effect, 0)

	evening := rows[18]
	near(t, "30/30 fuel", evening.Econ.GenFuelCostUah, 500)
	near(t, "30/30 effect = island half-hour", evening.Econ.Effect, 500)
	near(t, "30/30 ESS share", evening.Econ.EssOffgridValueUah, 1000.0/3)
	near(t, "30/30 PV share", evening.Econ.PvOffgridValueUah, 500.0/3)
	near(t, "30/30 ess_net", evening.Econ.EssNet, 1000.0/3)

	tot := ComputeDailyTotals(rows)
	near(t, "day effect", tot.Effect, 0+40+0+500+20)
	near(t, "EBITDA = effect + wear", tot.Ebitda, tot.Effect)
	near(t, "fuel", tot.GenFuelCost, 2500)
	near(t, "litres", tot.GenFuelL, 50)
	if tot.GenFuelMethod != GenFuelPerHour {
		t.Fatalf("method = %q", tot.GenFuelMethod)
	}
}

// With wear priced the identity still holds: EBITDA = effect + wear.
func TestGenEbitdaWithWear(t *testing.T) {
	for _, method := range []string{GenFuelPerKwh, GenFuelPerHour} {
		tf := genTariffs(method)
		tf.DegradationUahPerKwh = 0.6
		rows, _ := AssembleDay(genDayInput(t, tf, true))
		tot := ComputeDailyTotals(rows)
		near(t, method+" EBITDA", tot.Ebitda, tot.Effect+tot.EssDischarged*0.6)
	}
}

// The first hour of generator data can start mid-hour: the allocator then
// saw the island half only, and scaling its battery split onto the whole
// hour's discharge counter claims every discharged kWh as off the grid.
// The off-grid load bounds the claim, so EBITDA stays effect + wear.
func TestGenMixedHourBoundedByOffgridLoad(t *testing.T) {
	tf := genTariffs(GenFuelPerKwh)
	tf.DegradationUahPerKwh = 0.3
	start := time.Date(2026, 10, 20, 22, 0, 0, 0, time.UTC)
	flow := HourFlows{
		GridImport: 44.6, EssDischarged: 245.6, EssToLoad: 245.6,
		Gen: GenFlows{
			Accounted: true, LoadOffgrid: 154.4, EssToLoadOffgrid: 245.6, EssToLoadIsland: 245.6,
			Minutes: SupplyMinutes{Island: 30},
		},
	}
	row := &HourRow{HourStart: start, Rdn: ptr(9.8), Flow: flow, Econ: HourEconomicsFor(9.8, flow, tf)}
	e := row.Econ
	near(t, "load", e.Load, 290.2)
	near(t, "load off grid", e.LoadOffgrid, 154.4)
	near(t, "battery off grid ≤ the off-grid load", e.EssToLoadOffgrid, 154.4)
	near(t, "V_ess_off", e.EssOffgridValueUah, 154.4*15)
	tot := ComputeDailyTotals([]*HourRow{row})
	near(t, "EBITDA = effect + wear", tot.Ebitda, tot.Effect+245.6*0.3)
}

// Rebalancing scales the load the grid counters imply; an hour counted
// with the generator has its load from the generator's meter and keeps it.
func TestGenHoursStayOutOfRebalance(t *testing.T) {
	gen := &HourRow{Rdn: ptr(10), Flow: HourFlows{Gen: GenFlows{Accounted: true, GenKwh: 100}}, Econ: HourEconomics{Load: 100, BaselineCost: 2500, ActualCost: 2500}}
	shifted := &HourRow{Rdn: ptr(10), Flow: HourFlows{EssCharged: 20}, Econ: HourEconomics{}}
	late := &HourRow{Rdn: ptr(10), Flow: HourFlows{GridImport: 50}, Econ: HourEconomics{Load: 50, BaselineCost: 500, ActualCost: 500}}
	if !rebalanceDailyLoad([]*HourRow{gen, shifted, late}) {
		t.Fatal("the grid hours carry 20 kWh of phantom load")
	}
	near(t, "generator hour load", gen.Econ.Load, 100)
	near(t, "generator hour effect", gen.Econ.Effect, 0)
	near(t, "grid hour load", late.Econ.Load, 30)
}

func TestRollHourGenerator(t *testing.T) {
	charged := rollHour(EssState{}, HourFlows{EssCharged: 50, Gen: GenFlows{Accounted: true, GenToEss: 50}},
		rollPrices{importPrice: 2, exportPrice: 2, genChargeCostUah: 750})
	near(t, "kWh", charged.Next.Kwh, 50)
	near(t, "UAH", charged.Next.Uah, 750)
	out := rollHour(charged.Next, HourFlows{EssDischarged: 20, EssToLoad: 20, Gen: GenFlows{Accounted: true}},
		rollPrices{importPrice: 2, exportPrice: 2, essToLoadOffgrid: 20, essOffgridValueUah: 300})
	near(t, "withdrawn", out.WithdrawnCostUah, 300)
	near(t, "realized (diesel valuation)", out.RealizedProfitUah, 0)
}

func TestReconcileScalesGenCharge(t *testing.T) {
	f := &HourFlows{
		PV: 0, EssCharged: 50, EssDischarged: 0,
		Gen: GenFlows{Accounted: true, GenKwh: 150, GenToEss: 50, GenToLoad: 100, LoadOffgrid: 100},
	}
	// Load 150 − 40 = 110, of it 100 off the grid: use_power 10 matches.
	recon := reconcileFlows([]*HourFlows{f}, &CanonicalDaily{EssCharged: 40, Load: 10})
	if !recon.Applied {
		t.Fatal("reconcile should apply")
	}
	near(t, "charge", f.EssCharged, 40)
	near(t, "gen_to_ess follows the charge", f.Gen.GenToEss, 40)
	near(t, "G keeps its meter", f.Gen.GenKwh, 150)
	near(t, "gen_to_load", f.Gen.GenToLoad, 110)
	for _, fl := range recon.Flags {
		if strings.HasPrefix(fl, "load_mismatch") {
			t.Fatalf("the off-grid load stays out of the use_power check: %v", recon.Flags)
		}
	}
}

func serviceGenBackend(t *testing.T, schedule Schedule, dates ...string) *fakeBackend {
	t.Helper()
	b, loc := newKyivBackend(t)
	b.schedule = schedule
	for _, d := range dates {
		day, _ := time.ParseInLocation("2006-01-02", d, loc)
		flows, deltas := genDayFlows(day)
		b.flows[d] = flows
		b.deltas = append(b.deltas, deltas...)
		b.dam = append(b.dam, fullDayDAM(d)...)
	}
	return b
}

func accountedHours(day StoredDay) int {
	n := 0
	for _, r := range day.Rows {
		if r != nil && r.Flow.Gen.Accounted {
			n++
		}
	}
	return n
}

func TestServiceGenCostMissing(t *testing.T) {
	b := serviceGenBackend(t, Schedule{{EffectiveFrom: mustDate("1970-01-01"), Tariffs: flatTariffs}}, "2026-10-20")
	day, err := NewService(b).ComputeDay(context.Background(), "org1", "2026-10-20", "Europe/Kyiv")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(day.Totals.QualityFlags, "gen_cost_missing") {
		t.Fatalf("flags = %v, want gen_cost_missing", day.Totals.QualityFlags)
	}
	if n := accountedHours(day); n != 0 {
		t.Fatalf("%d hours counted with the generator, want none", n)
	}
}

// A fuel version counts the generator from its own date, and a later
// version switches the method from its date.
func TestServiceGenFuelVersionFromItsDate(t *testing.T) {
	schedule := Schedule{
		{EffectiveFrom: mustDate("1970-01-01"), Tariffs: flatTariffs},
		{EffectiveFrom: mustDate("2026-10-20"), Tariffs: genTariffs(GenFuelPerKwh)},
		{EffectiveFrom: mustDate("2026-10-25"), Tariffs: genTariffs(GenFuelPerHour)},
	}
	b := serviceGenBackend(t, schedule, "2026-10-19", "2026-10-20", "2026-10-25")
	svc := NewService(b)
	compute := func(date string) StoredDay {
		day, err := svc.ComputeDay(context.Background(), "org1", date, "Europe/Kyiv")
		if err != nil {
			t.Fatal(err)
		}
		return day
	}
	before := compute("2026-10-19")
	if accountedHours(before) != 0 || !slices.Contains(before.Totals.QualityFlags, "gen_cost_missing") {
		t.Fatalf("before the version: %d accounted, flags %v", accountedHours(before), before.Totals.QualityFlags)
	}
	perKwh := compute("2026-10-20")
	if accountedHours(perKwh) != 3 || perKwh.Totals.GenFuelMethod != GenFuelPerKwh {
		t.Fatalf("per_kwh day: %d accounted, method %q", accountedHours(perKwh), perKwh.Totals.GenFuelMethod)
	}
	if slices.Contains(perKwh.Totals.QualityFlags, "gen_cost_missing") {
		t.Fatalf("flags = %v", perKwh.Totals.QualityFlags)
	}
	perHour := compute("2026-10-25")
	if perHour.Totals.GenFuelMethod != GenFuelPerHour {
		t.Fatalf("per_hour day: method %q", perHour.Totals.GenFuelMethod)
	}
}

// The group's shared generator stays out until the group is computed as
// one: its members keep today's formulas whatever their tariff says.
func TestServiceGenSharedGroupMember(t *testing.T) {
	b := serviceGenBackend(t, Schedule{{EffectiveFrom: mustDate("1970-01-01"), Tariffs: genTariffs(GenFuelPerKwh)}}, "2026-10-20")
	day, err := NewService(b).ComputeDay(context.Background(), "de", "2026-10-20", "Europe/Kyiv")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(day.Totals.QualityFlags, "gen_shared") {
		t.Fatalf("flags = %v, want gen_shared", day.Totals.QualityFlags)
	}
	if n := accountedHours(day); n != 0 {
		t.Fatalf("%d hours counted with a shared generator", n)
	}
}

func TestGenFlags(t *testing.T) {
	row := func(g GenFlows, kwh float64, est bool, m SupplyMinutes) *HourRow {
		return &HourRow{Flow: HourFlows{Gen: g}, GenKwh: kwh, GenEstimated: est, SupplyMinutes: m}
	}
	rows := []*HourRow{
		row(GenFlows{Accounted: true}, 70, true, SupplyMinutes{Generator: 60}),
		row(GenFlows{}, 0, false, SupplyMinutes{Transition: 2, Unknown: 5}),
		nil,
	}
	got := genFlags(rows, true, false)
	want := []string{"gen_estimated", "supply_unknown:5", "supply_transition:2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("flags = %v, want %v", got, want)
	}
	if got := genFlags(rows[1:], true, false); got != nil {
		t.Fatalf("a day the generator never ran has no flags, got %v", got)
	}
	blackout := []*HourRow{row(GenFlows{}, 0, false, SupplyMinutes{Blackout: 3})}
	if got := genFlags(blackout, false, false); got != nil {
		t.Fatalf("a blackout alone has no load to price, got %v", got)
	}
	island := []*HourRow{row(GenFlows{}, 0, false, SupplyMinutes{Island: 20})}
	if got := genFlags(island, false, false); !reflect.DeepEqual(got, []string{"gen_cost_missing"}) {
		t.Fatalf("island without fuel: %v", got)
	}
}

// The optimum models a battery on the grid: an off-grid hour counted with
// the generator is in neither the УЗЕ fact nor the optimum, and the day's
// plan says why it holds.
func TestOptimumSkipsOffgridHours(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	hs := make([]HourlyRecord, 24)
	for h := range hs {
		hs[h] = HourlyRecord{HourStart: day.Add(time.Duration(h) * time.Hour), Rdn: floatPtr(5000), ImportPrice: 5, ExportPrice: 5}
	}
	hs[0].EssRemainingKwhStart = floatPtr(50)
	hs[19].EssDischarged, hs[19].EssToLoad, hs[19].EssNet = 20, 20, 100
	hs[19].GridToLoad = 50
	hs[21] = HourlyRecord{
		HourStart: day.Add(21 * time.Hour), Rdn: floatPtr(5000), ImportPrice: 5, ExportPrice: 5,
		EssDischarged: 20, EssToLoad: 20, EssNet: 300, Offgrid: true,
		EssToLoadOffgrid: 20, EssOffgridValueUah: 300,
	}
	days := []DailyRecord{{Day: day, IsFinal: true, Totals: DailyTotals{EssNet: 400, EssDischarged: 40, HoursWithData: 24}}}
	got := AggregateMonth("2026-06", loc, days, hs, fixedRatings(100, 0, 100, 0))
	near(t, "month fact", got.Totals.EssFact, 100)
	if len(got.Days) != 1 {
		t.Fatalf("days = %d", len(got.Days))
	}
	near(t, "day fact", got.Days[0].EssFact, 100)

	plan := planFor(t, withDate(hs, "2026-04-01"), loc)
	if plan.Hours[21].ReasonCode != ReasonOffgrid {
		t.Fatalf("21:00 reason = %q, want %q", plan.Hours[21].ReasonCode, ReasonOffgrid)
	}
	if plan.Hours[21].EssToLoadKwh != 0 || plan.Hours[21].GridToEssKwh != 0 {
		t.Fatalf("the plan must not trade off the grid: %+v", plan.Hours[21])
	}
}

// withDate moves a day of hourly records to another civil date.
func withDate(hs []HourlyRecord, date string) []HourlyRecord {
	to := mustDate(date)
	out := make([]HourlyRecord, len(hs))
	for i, h := range hs {
		d := h.HourStart
		h.HourStart = time.Date(to.Year(), to.Month(), to.Day(), d.Hour(), d.Minute(), 0, 0, d.Location())
		out[i] = h
	}
	return out
}

func TestScaleDayGenMoney(t *testing.T) {
	day := StoredDay{
		Rows: []*HourRow{{Econ: HourEconomics{
			GenFuelCostUah: 100, PvOffgridValueUah: 10, EssOffgridValueUah: 20, GenChargeCostUah: 30, GenFuelL: 7,
		}, GenKwh: 5}},
		Totals: DailyTotals{GenTotals: GenTotals{GenFuelCost: 100, ExpenseGenCharge: 30, GenFuelL: 7, GenKwh: 5}},
	}
	got := ScaleDay(day, 0.5)
	e := got.Rows[0].Econ
	near(t, "fuel", e.GenFuelCostUah, 50)
	near(t, "PV off-grid", e.PvOffgridValueUah, 5)
	near(t, "ESS off-grid", e.EssOffgridValueUah, 10)
	near(t, "charge", e.GenChargeCostUah, 15)
	near(t, "litres stay", e.GenFuelL, 7)
	near(t, "day fuel", got.Totals.GenFuelCost, 50)
	near(t, "day charge", got.Totals.ExpenseGenCharge, 15)
	near(t, "day litres stay", got.Totals.GenFuelL, 7)
	near(t, "day kWh stay", got.Totals.GenKwh, 5)
	near(t, "source untouched", day.Rows[0].Econ.GenFuelCostUah, 100)
}

func TestGenTotalsMixedMethod(t *testing.T) {
	var g GenTotals
	g.add(GenTotals{GenFuelMethod: GenFuelPerKwh, GenFuelCost: 1})
	g.add(GenTotals{})
	if g.GenFuelMethod != GenFuelPerKwh {
		t.Fatalf("method = %q", g.GenFuelMethod)
	}
	g.add(GenTotals{GenFuelMethod: GenFuelPerHour, GenFuelCost: 2})
	if g.GenFuelMethod != GenFuelMixed || math.Abs(g.GenFuelCost-3) > 1e-12 {
		t.Fatalf("got %+v", g)
	}
}

// A month adds its days' generator parts up and books the fuel spent on
// charging as an expense, so the month's EBITDA is the sum of its days'.
func TestAggregateMonthGenTotals(t *testing.T) {
	loc := time.UTC
	d1 := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	d2 := d1.AddDate(0, 0, 1)
	days := []DailyRecord{
		{Day: d1, Totals: DailyTotals{HoursWithData: 24, RevenuePvSelf: 100, Ebitda: 60, GenTotals: GenTotals{GenKwh: 10, ExpenseGenCharge: 40, GenFuelMethod: GenFuelPerKwh}}},
		{Day: d2, Totals: DailyTotals{HoursWithData: 24, RevenuePvSelf: 50, Ebitda: 50, GenTotals: GenTotals{GenKwh: 5, GenFuelMethod: GenFuelPerHour}}},
	}
	got := AggregateMonth("2026-06", loc, days, nil, fixedRatings(100, 0, 100, 0))
	near(t, "gen kWh", got.Totals.GenKwh, 15)
	near(t, "EBITDA", got.Totals.Ebitda, 110)
	if got.Totals.GenFuelMethod != GenFuelMixed {
		t.Fatalf("method = %q", got.Totals.GenFuelMethod)
	}
}
