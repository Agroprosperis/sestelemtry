package economics

import (
	"context"
	"math"
	"testing"
	"time"
)

func floatPtr(v float64) *float64 { return &v }

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
		{HourStart: day1.Add(18 * time.Hour), Rdn: floatPtr(8), GridImport: 100, EssNet: 80, EssDischarged: 10},
		{HourStart: day1.Add(20 * time.Hour), Rdn: floatPtr(12), GridImport: 100, EssNet: 40, EssDischarged: 20},
		{HourStart: day2.Add(19 * time.Hour), Rdn: floatPtr(15), GridImport: 300, EssNet: 90, EssDischarged: 30},
	}

	got := AggregateMonth("2026-06", loc, days, hourly, 100, 0.6, 0, 0)

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
	// day1 hour 18: margin = essNet/essDischarged = 80/10 = 8.
	if h18 := got.HourlyMargin[0].Hours[18]; h18 == nil || math.Abs(*h18-8) > 1e-9 {
		t.Fatalf("day1 hour18 margin = %v, want 8", h18)
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
