package economics

import (
	"context"
	"math"
	"testing"
	"time"
)

// TestAggregateYearSumsMonthsAndQuarters checks that the year totals are
// the sum of the per-month rollups, that the equivalent cycles sum, that
// the quarter cards bucket months correctly, and that the month x hour
// marginality heatmap is built per month.
func TestAggregateYearSumsMonthsAndQuarters(t *testing.T) {
	loc := time.UTC
	jan := time.Date(2026, 1, 15, 0, 0, 0, 0, loc)
	feb := time.Date(2026, 2, 10, 0, 0, 0, 0, loc)
	apr := time.Date(2026, 4, 5, 0, 0, 0, 0, loc)

	days := []DailyRecord{
		{
			Day: jan, IsFinal: true,
			Totals: DailyTotals{
				BaselineCost: 1000, ActualCost: 400, Effect: 600, EssNet: 120,
				Load: 500, PV: 800, GridImport: 200, GridExport: 100,
				EssCharged: 50, EssDischarged: 40,
				PVToLoad: 300, PVToEss: 20, PVToGrid: 80,
				GridToLoad: 180, GridToEss: 20, EssToLoad: 30, EssToGrid: 10,
				AvgImportPrice: 10, AvgExportPrice: 5,
				RevenuePvExport: 40, RevenuePvSelf: 300, RevenueEssExport: 50, RevenueEssSelf: 60,
				ExpenseGridCharge: 30, HoursWithData: 24,
				EssAvgCostBasisEod: 7, EssResidualKwhEod: 100, EssCostBasisUahEod: 700,
			},
		},
		{
			Day: feb, IsFinal: true,
			Totals: DailyTotals{
				BaselineCost: 2000, ActualCost: 900, Effect: 1100, EssNet: 200,
				Load: 600, PV: 900, GridImport: 300, GridExport: 50,
				EssCharged: 60, EssDischarged: 50,
				PVToLoad: 400, PVToEss: 30, PVToGrid: 20,
				GridToLoad: 270, GridToEss: 30, EssToLoad: 40, EssToGrid: 5,
				AvgImportPrice: 20, AvgExportPrice: 6,
				RevenuePvExport: 12, RevenuePvSelf: 400, RevenueEssExport: 30, RevenueEssSelf: 80,
				ExpenseGridCharge: 60, HoursWithData: 24,
				EssAvgCostBasisEod: 9, EssResidualKwhEod: 120, EssCostBasisUahEod: 1080,
			},
		},
		{
			Day: apr, IsFinal: true,
			Totals: DailyTotals{
				BaselineCost: 500, ActualCost: 200, Effect: 300, EssNet: 70,
				Load: 300, PV: 500, GridImport: 100, GridExport: 40,
				EssCharged: 25, EssDischarged: 20,
				PVToLoad: 150, PVToEss: 10, PVToGrid: 40,
				GridToLoad: 90, GridToEss: 10, EssToLoad: 15, EssToGrid: 5,
				AvgImportPrice: 15, AvgExportPrice: 4,
				RevenuePvExport: 8, RevenuePvSelf: 150, RevenueEssExport: 12, RevenueEssSelf: 30,
				ExpenseGridCharge: 20, HoursWithData: 24,
				EssAvgCostBasisEod: 8, EssResidualKwhEod: 80, EssCostBasisUahEod: 640,
			},
		},
	}

	hourly := []HourlyRecord{
		{
			HourStart: jan.Add(19 * time.Hour), Rdn: floatPtr(12), GridImport: 100,
			EssNet: 90, EssDischarged: 30, EssToLoad: 30, ImportPrice: 10,
			EssWithdrawnCostUah: floatPtr(200), EssRealizedProfitUah: floatPtr(90),
		},
		{
			HourStart: feb.Add(20 * time.Hour), Rdn: floatPtr(18), GridImport: 200,
			EssNet: 60, EssDischarged: 20, EssToLoad: 20, ImportPrice: 9,
			EssWithdrawnCostUah: floatPtr(100), EssRealizedProfitUah: floatPtr(60),
		},
	}

	got := AggregateYear("2026", loc, days, hourly, fixedRatings(100, 0.6, 0, 0))

	if len(got.Months) != 12 {
		t.Fatalf("Months len = %d, want 12", len(got.Months))
	}
	if got.MonthsWithData != 3 {
		t.Fatalf("MonthsWithData = %d, want 3", got.MonthsWithData)
	}

	// Year totals == sum of the months.
	var sumEffect, sumPV, sumDischarge, sumCycles float64
	for _, m := range got.Months {
		sumEffect += m.Totals.Effect
		sumPV += m.Totals.PV
		sumDischarge += m.Totals.EssDischarged
		sumCycles += m.Totals.EquivalentCycles
	}
	if math.Abs(got.Totals.Effect-sumEffect) > 1e-9 || got.Totals.Effect != 2000 {
		t.Fatalf("year Effect = %v, want sum %v (2000)", got.Totals.Effect, sumEffect)
	}
	if math.Abs(got.Totals.PV-sumPV) > 1e-9 || got.Totals.PV != 2200 {
		t.Fatalf("year PV = %v, want sum %v (2200)", got.Totals.PV, sumPV)
	}
	if got.Totals.EssDischarged != 110 {
		t.Fatalf("year EssDischarged = %v, want 110", got.Totals.EssDischarged)
	}
	// Cycles sum per month (each uses its own capacity): 0.4 + 0.5 + 0.2.
	if math.Abs(got.Totals.EquivalentCycles-sumCycles) > 1e-9 || math.Abs(got.Totals.EquivalentCycles-1.1) > 1e-9 {
		t.Fatalf("year EquivalentCycles = %v, want 1.1", got.Totals.EquivalentCycles)
	}

	// kWh-weighted import price across months:
	// (10*200 + 20*300 + 15*100) / 600 = 9500/600.
	if math.Abs(got.Totals.AvgImportPrice-(9500.0/600.0)) > 1e-9 {
		t.Fatalf("year AvgImportPrice = %v, want %v", got.Totals.AvgImportPrice, 9500.0/600.0)
	}

	// Quarters: Q1 = Jan+Feb, Q2 = Apr, Q3/Q4 empty.
	if len(got.Quarters) != 4 {
		t.Fatalf("Quarters len = %d, want 4", len(got.Quarters))
	}
	if got.Quarters[0].Quarter != 1 || math.Abs(got.Quarters[0].EffectUah-1700) > 1e-9 || math.Abs(got.Quarters[0].PvKwh-1700) > 1e-9 {
		t.Fatalf("Q1 = %+v, want effect 1700 / pv 1700", got.Quarters[0])
	}
	if got.Quarters[1].Quarter != 2 || math.Abs(got.Quarters[1].EffectUah-300) > 1e-9 || math.Abs(got.Quarters[1].PvKwh-500) > 1e-9 {
		t.Fatalf("Q2 = %+v, want effect 300 / pv 500", got.Quarters[1])
	}
	if got.Quarters[2].EffectUah != 0 || got.Quarters[3].EffectUah != 0 {
		t.Fatalf("Q3/Q4 effect = %v/%v, want 0/0", got.Quarters[2].EffectUah, got.Quarters[3].EffectUah)
	}

	// Best / worst day across the year.
	if got.Totals.BestDay.Date != "2026-02-10" || got.Totals.BestDay.EffectUah != 1100 {
		t.Fatalf("BestDay = %+v, want 2026-02-10/1100", got.Totals.BestDay)
	}
	if got.Totals.MinEffectDay.Date != "2026-04-05" || got.Totals.MinEffectDay.EffectUah != 300 {
		t.Fatalf("MinEffectDay = %+v, want 2026-04-05/300", got.Totals.MinEffectDay)
	}

	// EOD snapshot from the last month with data (April).
	if got.Totals.EssResidualKwhEod != 80 || got.Totals.EssCostBasisUahEod != 640 {
		t.Fatalf("EOD snapshot = %v/%v, want 80/640", got.Totals.EssResidualKwhEod, got.Totals.EssCostBasisUahEod)
	}

	// Monthly margin heatmap: 12 rows, 24 hours each. Jan hour 19 margin =
	// realized profit / discharged = 90/30 = 3; Feb hour 20 = 60/20 = 3.
	if len(got.MonthlyMargin) != 12 {
		t.Fatalf("MonthlyMargin rows = %d, want 12", len(got.MonthlyMargin))
	}
	janRow := got.MonthlyMargin[0]
	if janRow.Month != "2026-01" || len(janRow.Hours) != 24 {
		t.Fatalf("Jan margin row = %+v, want 2026-01 with 24 hours", janRow)
	}
	h19 := janRow.Hours[19]
	if h19 == nil || math.Abs(h19.Margin-3) > 1e-9 {
		t.Fatalf("Jan hour19 margin = %v, want 3", h19)
	}
	// The hover breakdown must reconstruct the headline: revenue 30x10=300,
	// cost 200, wear the 10 the persisted profit implies.
	if math.Abs(h19.RevenueUah-300) > 1e-9 || math.Abs(h19.CostUah-200) > 1e-9 ||
		math.Abs(h19.WearUah-10) > 1e-9 || math.Abs(h19.DischargedKwh-30) > 1e-9 {
		t.Fatalf("Jan hour19 breakdown = %+v, want 30 кВт·год / 300 / 200 / 10", *h19)
	}
	if h := got.MonthlyMargin[1].Hours[20]; h == nil || math.Abs(h.Margin-3) > 1e-9 {
		t.Fatalf("Feb hour20 margin = %v, want 3", h)
	}
	// A no-discharge hour stays nil.
	if h := janRow.Hours[3]; h != nil {
		t.Fatalf("Jan hour3 margin = %v, want nil", h)
	}
}

// TestAnnualHeatmapSumsHourAcrossDays pins the annual cell to a
// kWh-weighted month total rather than an average of daily ratios: a
// small brilliant hour must not outweigh a big mediocre one, and the
// breakdown has to add up to the number the cell prints.
func TestAnnualHeatmapSumsHourAcrossDays(t *testing.T) {
	loc := time.UTC
	d1 := time.Date(2026, 3, 4, 0, 0, 0, 0, loc)
	d2 := time.Date(2026, 3, 5, 0, 0, 0, 0, loc)

	days := []DailyRecord{
		{Day: d1, IsFinal: true, Totals: DailyTotals{Effect: 100, HoursWithData: 24, EssDischarged: 10}},
		{Day: d2, IsFinal: true, Totals: DailyTotals{Effect: 100, HoursWithData: 24, EssDischarged: 40}},
	}
	hourly := []HourlyRecord{
		{
			HourStart: d1.Add(18 * time.Hour), EssDischarged: 10, EssToLoad: 10, ImportPrice: 12,
			EssWithdrawnCostUah: floatPtr(20), EssRealizedProfitUah: floatPtr(90),
		},
		{
			HourStart: d2.Add(18 * time.Hour), EssDischarged: 40, EssToLoad: 40, ImportPrice: 6,
			EssWithdrawnCostUah: floatPtr(180), EssRealizedProfitUah: floatPtr(30),
		},
	}

	got := AggregateYear("2026", loc, days, hourly, fixedRatings(100, 0.6, 0, 0))

	cell := got.MonthlyMargin[2].Hours[18]
	if cell == nil {
		t.Fatalf("March hour18 cell is nil, want the two days summed")
	}
	// (90 + 30) / (10 + 40) = 2,4 — not (9 + 0,75) / 2.
	if math.Abs(cell.Margin-2.4) > 1e-9 || math.Abs(cell.DischargedKwh-50) > 1e-9 {
		t.Fatalf("March hour18 = %+v, want margin 2.4 over 50 кВт·год", *cell)
	}
	if math.Abs(cell.Margin*cell.DischargedKwh-(cell.RevenueUah-cell.CostUah-cell.WearUah)) > 1e-9 {
		t.Fatalf("breakdown does not add up to the margin: %+v", *cell)
	}
}

// TestAggregateYearSumsPvExportPotential checks that the "sell
// everything to the grid" yardstick carries into the year: each month
// prices its own yield at its own hourly export prices, and the year is
// the plain sum.
func TestAggregateYearSumsPvExportPotential(t *testing.T) {
	loc := time.UTC
	apr := time.Date(2026, 4, 10, 0, 0, 0, 0, loc)
	jul := time.Date(2026, 7, 10, 0, 0, 0, 0, loc)

	days := []DailyRecord{
		{Day: apr, IsFinal: true, Totals: DailyTotals{HoursWithData: 24, PV: 90}},
		{Day: jul, IsFinal: true, Totals: DailyTotals{HoursWithData: 24, PV: 200}},
	}
	hourly := []HourlyRecord{
		{
			HourStart: apr.Add(12 * time.Hour), Rdn: floatPtr(5), ImportPrice: 10, ExportPrice: 5,
			PV: 90, PVToLoad: 60, PVToGrid: 30,
		},
		{
			HourStart: jul.Add(13 * time.Hour), Rdn: floatPtr(3), ImportPrice: 8, ExportPrice: 3,
			PV: 200, PVToLoad: 120, PVToEss: 50, PVToGrid: 30,
		},
	}

	got := AggregateYear("2026", loc, days, hourly, fixedRatings(100, 0.6, 0, 0))

	want := 90*5.0 + 200*3.0
	if math.Abs(got.Totals.PvExportPotential-want) > 1e-6 {
		t.Fatalf("PvExportPotential = %v, want %v", got.Totals.PvExportPotential, want)
	}
	if math.Abs(got.Months[3].Totals.PvExportPotential-450) > 1e-6 {
		t.Fatalf("April PvExportPotential = %v, want 450", got.Months[3].Totals.PvExportPotential)
	}
	// One priced hour per month leaves the battery nothing to shift to,
	// so the merchant-with-УЗЕ figure must collapse to the bare one.
	if math.Abs(got.Totals.PvEssExportPotential-want) > 1e-6 {
		t.Fatalf("PvEssExportPotential = %v, want %v", got.Totals.PvEssExportPotential, want)
	}
}

// TestAggregatePeriodSlidingWindow checks that an arbitrary month window
// crossing a year boundary buckets quarters by (year, quarter) in order
// and sums the totals over only the windowed months.
func TestAggregatePeriodSlidingWindow(t *testing.T) {
	loc := time.UTC
	nov := time.Date(2025, 11, 12, 0, 0, 0, 0, loc)
	jan := time.Date(2026, 1, 8, 0, 0, 0, 0, loc)
	// A day in October 2025 must be ignored: it's outside the window.
	oct := time.Date(2025, 10, 3, 0, 0, 0, 0, loc)

	mk := func(when time.Time, effect, pv float64) DailyRecord {
		return DailyRecord{
			Day: when, IsFinal: true,
			Totals: DailyTotals{Effect: effect, PV: pv, HoursWithData: 24},
		}
	}
	days := []DailyRecord{mk(oct, 999, 999), mk(nov, 400, 800), mk(jan, 300, 600)}

	keys := []string{"2025-11", "2025-12", "2026-01", "2026-02"}
	got := AggregatePeriod("2025-11..2026-02", keys, loc, days, nil, fixedRatings(100, 0.6, 0, 0))

	if len(got.Months) != 4 || got.From != "2025-11" || got.To != "2026-02" {
		t.Fatalf("window months=%d from=%q to=%q, want 4 / 2025-11 / 2026-02", len(got.Months), got.From, got.To)
	}
	if got.MonthsWithData != 2 {
		t.Fatalf("MonthsWithData = %d, want 2 (Nov, Jan)", got.MonthsWithData)
	}
	// October day is outside the window → not counted.
	if got.Totals.Effect != 700 {
		t.Fatalf("window Effect = %v, want 700 (400 + 300)", got.Totals.Effect)
	}
	// Quarters in appearance order: Q4 2025 (Nov+Dec), Q1 2026 (Jan+Feb).
	if len(got.Quarters) != 2 {
		t.Fatalf("Quarters len = %d, want 2", len(got.Quarters))
	}
	if got.Quarters[0].Year != 2025 || got.Quarters[0].Quarter != 4 || got.Quarters[0].EffectUah != 400 {
		t.Fatalf("Q[0] = %+v, want 2025/Q4/400", got.Quarters[0])
	}
	if got.Quarters[1].Year != 2026 || got.Quarters[1].Quarter != 1 || got.Quarters[1].EffectUah != 300 {
		t.Fatalf("Q[1] = %+v, want 2026/Q1/300", got.Quarters[1])
	}
}

// TestGetYearReadOnly verifies GetYear is a pure read: it serves whatever
// the daemon persisted across the calendar year, never recomputes, and
// rolls the stored months into the year totals.
func TestGetYearReadOnly(t *testing.T) {
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

	// Seed final days in two different months of 2020.
	for _, d := range []struct {
		when   time.Time
		effect float64
	}{
		{time.Date(2020, 3, 10, 0, 0, 0, 0, loc), 500},
		{time.Date(2020, 8, 20, 0, 0, 0, 0, loc), 700},
	} {
		b.SaveDay(context.Background(), StoredDay{
			Day:     d.when,
			IsFinal: true,
			Totals:  DailyTotals{Effect: d.effect, HoursWithData: 24},
		})
	}
	b.saveCount = 0

	year, err := svc.GetYear(context.Background(), "org-a", "2020", loc.String())
	if err != nil {
		t.Fatalf("GetYear: %v", err)
	}
	if year.Period != "2020" {
		t.Fatalf("Period = %q, want 2020", year.Period)
	}
	if b.saveCount != 0 {
		t.Fatalf("saveCount = %d, want 0 (read path must not recompute)", b.saveCount)
	}
	if len(year.Months) != 12 {
		t.Fatalf("Months = %d, want 12", len(year.Months))
	}
	if year.MonthsWithData != 2 {
		t.Fatalf("MonthsWithData = %d, want 2", year.MonthsWithData)
	}
	if year.Totals.Effect != 1200 {
		t.Fatalf("year Effect = %v, want 1200 (500 + 700)", year.Totals.Effect)
	}
	// March is Q1, August is Q3.
	if year.Quarters[0].EffectUah != 500 {
		t.Fatalf("Q1 effect = %v, want 500", year.Quarters[0].EffectUah)
	}
	if year.Quarters[2].EffectUah != 700 {
		t.Fatalf("Q3 effect = %v, want 700", year.Quarters[2].EffectUah)
	}
}
