package economics

import (
	"context"
	"math"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }

// TestAggregateMonthCountsFlaggedDays pins which day-level quality flags
// mark a day as approximate in the month rollup: counter-issue flags
// (import_lag / load_mismatch / reconcile_rejected) count, routine
// bookkeeping flags (no_scale, load_rebalanced) don't.
func TestAggregateMonthCountsFlaggedDays(t *testing.T) {
	loc := time.UTC
	mk := func(day int, flags ...string) DailyRecord {
		return DailyRecord{
			Day:    time.Date(2026, 1, day, 0, 0, 0, 0, loc),
			Totals: DailyTotals{HoursWithData: 24, QualityFlags: flags},
		}
	}
	days := []DailyRecord{
		mk(22, "no_scale:grid_export", "load_mismatch:0.5197", "load_rebalanced"),
		mk(23, "import_lag:512"),
		mk(24, "no_scale:grid_export", "load_rebalanced"),
		mk(25),
		mk(26, "counter_step:pv:0.0313"),
	}
	got := AggregateMonth("2026-01", loc, days, nil, fixedRatings(100, 0.6, 0, 0))
	if got.Totals.FlaggedDays != 3 {
		t.Fatalf("FlaggedDays = %d, want 3", got.Totals.FlaggedDays)
	}
	if got.Totals.DaysWithData != 5 {
		t.Fatalf("DaysWithData = %d, want 5", got.Totals.DaysWithData)
	}
}

// TestAggregateMonthSumsAndWeightedPrices checks the additive rollup,
// the kWh-weighted price reconstruction, the EOD-from-last-day snapshot,
// the best/min-day extremes, and the equivalent-cycle metric.
func TestAggregateMonthSumsAndWeightedPrices(t *testing.T) {
	loc := time.UTC
	day1 := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	day2 := time.Date(2026, 6, 2, 0, 0, 0, 0, loc)

	days := []DailyRecord{
		{
			Day:     day1,
			IsFinal: true,
			Totals: DailyTotals{
				BaselineCost: 1000, ActualCost: 400, Effect: 600, EssNet: 120,
				Load: 500, PV: 800, GridImport: 200, GridExport: 100,
				EssCharged: 50, EssDischarged: 40,
				PVToLoad: 300, PVToEss: 20, PVToGrid: 80,
				GridToLoad: 180, GridToEss: 20, EssToLoad: 30, EssToGrid: 10,
				AvgImportPrice: 10, AvgExportPrice: 5,
				RevenuePvExport: 40, RevenuePvSelf: 300, RevenueEssExport: 50, RevenueEssSelf: 60,
				ExpenseGridCharge: 30,
				HoursWithData:     24,
				EssAvgCostBasisEod: 7, EssResidualKwhEod: 100, EssCostBasisUahEod: 700,
			},
		},
		{
			Day:     day2,
			IsFinal: true,
			Totals: DailyTotals{
				BaselineCost: 2000, ActualCost: 900, Effect: 1100, EssNet: 200,
				Load: 600, PV: 900, GridImport: 300, GridExport: 50,
				EssCharged: 60, EssDischarged: 50,
				PVToLoad: 400, PVToEss: 30, PVToGrid: 20,
				GridToLoad: 270, GridToEss: 30, EssToLoad: 40, EssToGrid: 5,
				AvgImportPrice: 20, AvgExportPrice: 6,
				RevenuePvExport: 12, RevenuePvSelf: 400, RevenueEssExport: 30, RevenueEssSelf: 80,
				ExpenseGridCharge: 60,
				HoursWithData:     24,
				EssAvgCostBasisEod: 9, EssResidualKwhEod: 120, EssCostBasisUahEod: 1080,
			},
		},
	}

	hourly := []HourlyRecord{
		{
			HourStart: day1.Add(18 * time.Hour), Rdn: floatPtr(8), ImportPrice: 8, GridImport: 100,
			EssToLoad: 10, EssNet: 80, EssDischarged: 10,
			EssRealizedProfitUah: floatPtr(80), EssWithdrawnCostUah: floatPtr(0),
		},
		{
			HourStart: day1.Add(20 * time.Hour), Rdn: floatPtr(12), ImportPrice: 12, GridImport: 100,
			EssToLoad: 20, EssNet: 40, EssDischarged: 20,
			EssRealizedProfitUah: floatPtr(40), EssWithdrawnCostUah: floatPtr(200),
		},
		{
			HourStart: day2.Add(19 * time.Hour), Rdn: floatPtr(15), ImportPrice: 15, GridImport: 300,
			EssToLoad: 30, EssNet: 90, EssDischarged: 30,
			EssRealizedProfitUah: floatPtr(90), EssWithdrawnCostUah: floatPtr(360),
		},
	}

	got := AggregateMonth("2026-06", loc, days, hourly, fixedRatings(100, 0.6, 0, 0))

	if got.Totals.Effect != 1700 {
		t.Fatalf("Effect sum = %v, want 1700", got.Totals.Effect)
	}
	if got.Totals.BaselineCost != 3000 || got.Totals.ActualCost != 1300 {
		t.Fatalf("cost sums = %v / %v, want 3000 / 1300", got.Totals.BaselineCost, got.Totals.ActualCost)
	}
	if got.Totals.EssDischarged != 90 {
		t.Fatalf("EssDischarged = %v, want 90", got.Totals.EssDischarged)
	}
	// kWh-weighted import price: (10*200 + 20*300) / (200+300) = 8000/500 = 16.
	if math.Abs(got.Totals.AvgImportPrice-16) > 1e-9 {
		t.Fatalf("AvgImportPrice = %v, want 16", got.Totals.AvgImportPrice)
	}
	// kWh-weighted export price: (5*100 + 6*50) / 150 = 800/150 ≈ 5.333.
	if math.Abs(got.Totals.AvgExportPrice-(800.0/150.0)) > 1e-9 {
		t.Fatalf("AvgExportPrice = %v, want %v", got.Totals.AvgExportPrice, 800.0/150.0)
	}
	// RDN weighted by import: (8*100 + 12*100 + 15*300) / 500 = 6500/500 = 13.
	if math.Abs(got.Totals.RdnAvgUahPerKwh-13) > 1e-9 {
		t.Fatalf("RdnAvg = %v, want 13", got.Totals.RdnAvgUahPerKwh)
	}
	if got.Totals.RdnMaxUahPerKwh != 15 {
		t.Fatalf("RdnMax = %v, want 15", got.Totals.RdnMaxUahPerKwh)
	}
	// Revenue total = sum of the four legs across both days.
	wantRevenue := 40.0 + 300 + 50 + 60 + 12 + 400 + 30 + 80
	if math.Abs(got.Totals.RevenueTotal-wantRevenue) > 1e-9 {
		t.Fatalf("RevenueTotal = %v, want %v", got.Totals.RevenueTotal, wantRevenue)
	}
	// EBITDA = revenue - expense(grid charge total = 90).
	if math.Abs(got.Totals.Ebitda-(wantRevenue-90)) > 1e-9 {
		t.Fatalf("Ebitda = %v, want %v", got.Totals.Ebitda, wantRevenue-90)
	}
	// Equivalent cycles: 90 discharged / 100 capacity = 0.9.
	if math.Abs(got.Totals.EquivalentCycles-0.9) > 1e-9 {
		t.Fatalf("EquivalentCycles = %v, want 0.9", got.Totals.EquivalentCycles)
	}
	// EOD snapshot from the last day with data (day2).
	if got.Totals.EssResidualKwhEod != 120 || got.Totals.EssCostBasisUahEod != 1080 {
		t.Fatalf("EOD snapshot = %v / %v, want 120 / 1080", got.Totals.EssResidualKwhEod, got.Totals.EssCostBasisUahEod)
	}
	if got.Totals.BestDay.Date != "2026-06-02" || got.Totals.BestDay.EffectUah != 1100 {
		t.Fatalf("BestDay = %+v, want 2026-06-02/1100", got.Totals.BestDay)
	}
	if got.Totals.MinEffectDay.Date != "2026-06-01" || got.Totals.MinEffectDay.EffectUah != 600 {
		t.Fatalf("MinEffectDay = %+v, want 2026-06-01/600", got.Totals.MinEffectDay)
	}
	if got.Totals.DaysWithData != 2 {
		t.Fatalf("DaysWithData = %d, want 2", got.Totals.DaysWithData)
	}
	if len(got.Days) != 2 {
		t.Fatalf("Days len = %d, want 2", len(got.Days))
	}
	// Per-day RDN avg for day1: (8*100 + 12*100)/200 = 10.
	if math.Abs(got.Days[0].RdnAvgUahPerKwh-10) > 1e-9 {
		t.Fatalf("day1 RdnAvg = %v, want 10", got.Days[0].RdnAvgUahPerKwh)
	}
	// Heatmap rows present, one per day, 24 hours each.
	if len(got.HourlyMargin) != 2 {
		t.Fatalf("HourlyMargin rows = %d, want 2", len(got.HourlyMargin))
	}
	if len(got.HourlyMargin[0].Hours) != 24 {
		t.Fatalf("heatmap day hours = %d, want 24", len(got.HourlyMargin[0].Hours))
	}
	// day1 hour 18: margin = realized profit / discharged = 80/10 = 8.
	if h18 := got.HourlyMargin[0].Hours[18]; h18 == nil || math.Abs(h18.Margin-8) > 1e-9 {
		t.Fatalf("day1 hour18 margin = %v, want 8", h18)
	}
}

// TestAggregateMonthPvExportPotential checks the "sell everything to the
// grid" yardstick: the whole PV yield priced at the export price of the
// hour that produced it, regardless of where the energy actually went.
// Hours without an RDN carry no export price and are skipped, as
// everywhere else in the rollup.
func TestAggregateMonthPvExportPotential(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	days := []DailyRecord{{Day: day, IsFinal: true, Totals: DailyTotals{HoursWithData: 24, PV: 260}}}
	hourly := []HourlyRecord{
		// Midday: only a sliver of the 150 kWh went to the grid, so the
		// potential must sit far above the exported leg.
		{
			HourStart: day.Add(12 * time.Hour), Rdn: floatPtr(4), ImportPrice: 9, ExportPrice: 4,
			PV: 150, PVToLoad: 100, PVToEss: 40, PVToGrid: 10,
		},
		// A rebalanced hour: PVToLoad was scaled down for phantom load, so
		// the split sums to 90 while the counter still reads 100. The
		// counter is what the plant could have sold.
		{
			HourStart: day.Add(16 * time.Hour), Rdn: floatPtr(6), ImportPrice: 11, ExportPrice: 6,
			PV: 100, PVToLoad: 70, PVToGrid: 20,
		},
		// No RDN: nothing to value this hour's yield at.
		{HourStart: day.Add(17 * time.Hour), PV: 10, PVToLoad: 10},
	}

	got := AggregateMonth("2026-06", loc, days, hourly, fixedRatings(100, 0.6, 0, 0))

	want := 150*4.0 + 100*6.0
	if math.Abs(got.Totals.PvExportPotential-want) > 1e-6 {
		t.Fatalf("PvExportPotential = %v, want %v", got.Totals.PvExportPotential, want)
	}
}

// TestAggregateMonthPvEssExportPotential checks the merchant-with-battery
// yardstick: rte 0.81 gives exact half-cycle efficiencies (√0.81 = 0.9),
// so storing the whole 90 kWh midday yield and selling it at the evening
// price is worth exactly 90 · 0.81 · 10 = 729 — more than the 549 gained
// over selling as produced (90 · 2 = 180), and the DP must find it.
func TestAggregateMonthPvEssExportPotential(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	days := []DailyRecord{{Day: day, IsFinal: true, Totals: DailyTotals{HoursWithData: 24, PV: 90}}}
	hourly := []HourlyRecord{
		{
			HourStart: day.Add(12 * time.Hour), Rdn: floatPtr(2), ImportPrice: 7, ExportPrice: 2,
			PV: 90, PVToGrid: 90,
		},
		{
			HourStart: day.Add(20 * time.Hour), Rdn: floatPtr(10), ImportPrice: 15, ExportPrice: 10,
		},
	}

	got := AggregateMonth("2026-06", loc, days, hourly, fixedRatings(100, 0, 100, 0.81))

	if math.Abs(got.Totals.PvExportPotential-180) > 1e-6 {
		t.Fatalf("PvExportPotential = %v, want 180", got.Totals.PvExportPotential)
	}
	if math.Abs(got.Totals.PvEssExportPotential-729) > 1e-6 {
		t.Fatalf("PvEssExportPotential = %v, want 729", got.Totals.PvEssExportPotential)
	}
}

// TestPvEssExportPotentialDegenerateCases pins the two ways the battery
// add-on must collapse to the bare potential: prices that fall over the
// day (nothing to shift forward to) and wear that eats the whole spread.
// A plant with no battery configured must also stay at the bare number.
func TestPvEssExportPotentialDegenerateCases(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	days := []DailyRecord{{Day: day, IsFinal: true, Totals: DailyTotals{HoursWithData: 24, PV: 90}}}
	mk := func(p1, p2 float64) []HourlyRecord {
		return []HourlyRecord{
			{HourStart: day.Add(12 * time.Hour), Rdn: floatPtr(p1), ImportPrice: p1 + 5, ExportPrice: p1, PV: 90, PVToGrid: 90},
			{HourStart: day.Add(20 * time.Hour), Rdn: floatPtr(p2), ImportPrice: p2 + 5, ExportPrice: p2},
		}
	}

	// Falling prices: the yield is already sold at the day's best hour.
	got := AggregateMonth("2026-06", loc, days, mk(10, 2), fixedRatings(100, 0, 100, 0.81))
	if math.Abs(got.Totals.PvEssExportPotential-got.Totals.PvExportPotential) > 1e-6 {
		t.Fatalf("falling prices: merchant %v != potential %v",
			got.Totals.PvEssExportPotential, got.Totals.PvExportPotential)
	}

	// Wear above the spread: shifting 1 kWh gains 0.81·10 − 2 = 6.1 but
	// costs 0.81·10 of degradation — idling wins.
	got = AggregateMonth("2026-06", loc, days, mk(2, 10), fixedRatings(100, 10, 100, 0.81))
	if math.Abs(got.Totals.PvEssExportPotential-got.Totals.PvExportPotential) > 1e-6 {
		t.Fatalf("wear: merchant %v != potential %v",
			got.Totals.PvEssExportPotential, got.Totals.PvExportPotential)
	}

	// No battery at all.
	got = AggregateMonth("2026-06", loc, days, mk(2, 10), fixedRatings(0, 0, 0, 0))
	if math.Abs(got.Totals.PvEssExportPotential-got.Totals.PvExportPotential) > 1e-6 {
		t.Fatalf("no ESS: merchant %v != potential %v",
			got.Totals.PvEssExportPotential, got.Totals.PvExportPotential)
	}
}

// TestHeatmapMarginIgnoresSameHourCharging pins the fix for the cells
// that used to read minus thousands of UAH/kWh: an hour that charges a
// full pack while trickling a little back out must be judged on what
// that trickle actually earned, not on the charging bill divided by it.
func TestHeatmapMarginIgnoresSameHourCharging(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)
	days := []DailyRecord{{Day: day, IsFinal: true, Totals: DailyTotals{HoursWithData: 24}}}
	hourly := []HourlyRecord{{
		HourStart: day.Add(15 * time.Hour), Rdn: floatPtr(10), ImportPrice: 10,
		GridToEss: 300, EssCharged: 300,
		// Half a kWh back out: EssNet is dominated by the 3000 UAH charge.
		EssDischarged: 0.5, EssToLoad: 0.5, EssNet: -2995,
		EssRealizedProfitUah: floatPtr(3), EssWithdrawnCostUah: floatPtr(1.7),
	}}

	got := AggregateMonth("2026-06", loc, days, hourly, fixedRatings(645, 0.6, 0, 0))

	if len(got.HourlyMargin) != 1 {
		t.Fatalf("HourlyMargin rows = %d, want 1", len(got.HourlyMargin))
	}
	// Below the discharge floor, so the hour gets no cell at all rather
	// than the -5990 UAH/kWh the old ratio produced.
	if h15 := got.HourlyMargin[0].Hours[15]; h15 != nil {
		t.Fatalf("hour15 margin = %v, want no cell", *h15)
	}

	// Same hour, same charging, but a real discharge: the cell now shows
	// what the withdrawn energy earned per kWh (6/4 = 1.5), untouched by
	// the 3000 UAH the pack spent filling up in the same hour.
	hourly[0].EssDischarged = 4
	hourly[0].EssToLoad = 4
	hourly[0].EssNet = -2960
	hourly[0].EssRealizedProfitUah = floatPtr(6)
	hourly[0].EssWithdrawnCostUah = floatPtr(31.6)
	got = AggregateMonth("2026-06", loc, days, hourly, fixedRatings(645, 0.6, 0, 0))
	h15 := got.HourlyMargin[0].Hours[15]
	if h15 == nil || math.Abs(h15.Margin-1.5) > 1e-9 {
		t.Fatalf("hour15 margin = %v, want 1.5", h15)
	}
	// The hover breakdown must reconstruct the headline: revenue 4x10=40,
	// cost 31.6, wear the 2.4 the persisted profit implies.
	if math.Abs(h15.RevenueUah-40) > 1e-9 || math.Abs(h15.CostUah-31.6) > 1e-9 ||
		math.Abs(h15.WearUah-2.4) > 1e-9 {
		t.Fatalf("breakdown = %+v, want revenue 40 / cost 31.6 / wear 2.4", *h15)
	}
	if math.Abs(h15.Margin*h15.DischargedKwh-(h15.RevenueUah-h15.CostUah-h15.WearUah)) > 1e-9 {
		t.Fatalf("breakdown does not add up to the margin: %+v", *h15)
	}
}

// TestAggregateMonthResolvesRatingsPerDay pins the mid-month expansion
// case: when a second УЗЕ pack comes online on the 15th, the month must
// judge each day by the plant it had that day. Freezing the 1st-of-month
// version used to flag the new pack's honest power as corrupt telemetry
// and rate its throughput against the old capacity.
func TestAggregateMonthResolvesRatingsPerDay(t *testing.T) {
	loc := time.UTC
	small := EssRatings{CapacityKwh: 100, PowerLimitKw: 100}
	big := EssRatings{CapacityKwh: 200, PowerLimitKw: 200}
	before := time.Date(2026, 6, 10, 0, 0, 0, 0, loc)
	after := time.Date(2026, 6, 20, 0, 0, 0, 0, loc)

	days := []DailyRecord{
		{Day: before, IsFinal: true, Totals: DailyTotals{HoursWithData: 24, EssDischarged: 80}},
		{Day: after, IsFinal: true, Totals: DailyTotals{HoursWithData: 24, EssDischarged: 180}},
	}
	// 180 kWh in one hour is impossible for the old 100 kW pack (limit
	// 100 · 1.5) but well inside the expanded one.
	hourly := []HourlyRecord{
		{
			HourStart: before.Add(19 * time.Hour), Rdn: floatPtr(10), ImportPrice: 10,
			EssToLoad: 80, EssNet: 800, EssDischarged: 80, EssPeakIntervalKw: 95,
			EssRealizedProfitUah: floatPtr(800), EssWithdrawnCostUah: floatPtr(0),
		},
		{
			HourStart: after.Add(19 * time.Hour), Rdn: floatPtr(10), ImportPrice: 10,
			EssToLoad: 180, EssNet: 1800, EssDischarged: 180, EssPeakIntervalKw: 190,
			EssRealizedProfitUah: floatPtr(1800), EssWithdrawnCostUah: floatPtr(0),
		},
	}
	ratingsFor := func(day time.Time) EssRatings {
		if day.Day() < 15 {
			return small
		}
		return big
	}

	got := AggregateMonth("2026-06", loc, days, hourly, ratingsFor)

	if got.Totals.EssDataQuality.AnomalousHours != 0 {
		t.Fatalf("AnomalousHours = %d, want 0: %+v",
			got.Totals.EssDataQuality.AnomalousHours, got.Totals.EssDataQuality.Anomalies)
	}
	// The reported ceiling is the one in force at the end of the month.
	if got.Totals.EssDataQuality.PowerLimitKwhPerInterval != 200 {
		t.Fatalf("PowerLimitKwhPerInterval = %v, want 200",
			got.Totals.EssDataQuality.PowerLimitKwhPerInterval)
	}
	// Cycles per day against that day's pack: 80/100 + 180/200 = 1.7,
	// not the 260/100 = 2.6 the frozen capacity produced.
	if math.Abs(got.Totals.EquivalentCycles-1.7) > 1e-9 {
		t.Fatalf("EquivalentCycles = %v, want 1.7", got.Totals.EquivalentCycles)
	}
	if math.Abs(got.Days[1].EquivalentCycles-0.9) > 1e-9 {
		t.Fatalf("day 20 cycles = %v, want 0.9", got.Days[1].EquivalentCycles)
	}
	// The heatmap keeps the expanded day's cell (1800/180 = 10 UAH/kWh).
	if h := got.HourlyMargin[1].Hours[19]; h == nil || math.Abs(h.Margin-10) > 1e-9 {
		t.Fatalf("day 20 hour19 margin = %v, want 10", h)
	}

	// Control: with the old ratings frozen over the whole month, the
	// expanded day's hour is thrown out as corrupt.
	frozen := AggregateMonth("2026-06", loc, days, hourly, constEssRatings(small))
	if frozen.Totals.EssDataQuality.AnomalousHours != 1 {
		t.Fatalf("frozen AnomalousHours = %d, want 1", frozen.Totals.EssDataQuality.AnomalousHours)
	}
}

// TestGetMonthReadOnly verifies GetMonth is a pure read: it serves
// whatever the daemon persisted and never recomputes days live, then
// aggregates the stored records.
func TestGetMonthReadOnly(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	b := &fakeBackend{
		flows: map[string][]FlowRow{},
		schedule: Schedule{{
			EffectiveFrom: mustDate("1970-01-01"),
			Tariffs:       flatTariffs,
		}},
	}
	svc := NewService(b)

	// Pre-seed a final day inside a past month so it is served from
	// cache without a recompute.
	seeded := time.Date(2020, 1, 5, 0, 0, 0, 0, loc)
	b.SaveDay(context.Background(), StoredDay{
		Day:     seeded,
		IsFinal: true,
		Totals:  DailyTotals{Effect: 999, HoursWithData: 24},
	})
	b.saveCount = 0

	month, err := svc.GetMonth(context.Background(), "org-a", "2020-01", loc.String())
	if err != nil {
		t.Fatalf("GetMonth: %v", err)
	}
	if month.Month != "2020-01" {
		t.Fatalf("Month = %q, want 2020-01", month.Month)
	}
	// Pure read: missing days are NOT recomputed, so nothing is saved.
	if b.saveCount != 0 {
		t.Fatalf("saveCount = %d, want 0 (read path must not recompute)", b.saveCount)
	}
	// The seeded day's effect must survive into the rollup.
	if month.Totals.Effect < 999 {
		t.Fatalf("Effect = %v, want >= 999 (seeded day included)", month.Totals.Effect)
	}
}
