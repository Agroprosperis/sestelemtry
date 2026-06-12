package economics

import (
	"math"
	"testing"
	"time"
)

// TestOptimizeDayArbitrage checks the classic charge-cheap / discharge-
// expensive case: with a 1-unit hour followed by a 10-unit hour, the
// optimizer should buy a full power-step in the cheap hour and sell it
// into load in the expensive one.
func TestOptimizeDayArbitrage(t *testing.T) {
	p := optimumParams{
		capacityKwh:          100,
		degradationUahPerKwh: 0,
		maxChargeKwh:         50,
		maxDischargeKwh:      50,
		socMinKwh:            0,
		socMaxKwh:            100,
		rte:                  1.0,
	}
	hours := make([]optimumHour, 24)
	hours[0] = optimumHour{tradable: true, importPrice: 1, exportPrice: 1, displaceableKwh: 100}
	hours[1] = optimumHour{tradable: true, importPrice: 10, exportPrice: 10, displaceableKwh: 100}

	got := optimizeDay(hours, 0, p, modeFull)
	// Charge 50 @1 (cost 50), discharge 50 to load @10 (revenue 500) → 450.
	if math.Abs(got-450) > 5 {
		t.Fatalf("optimizeDay = %v, want ~450", got)
	}
}

// TestOptimizeDayNoSpread: a flat price profile offers no arbitrage, so
// the optimum effect is zero (degradation would only make trading worse).
func TestOptimizeDayNoSpread(t *testing.T) {
	p := optimumParams{
		capacityKwh: 100, degradationUahPerKwh: 0.6,
		maxChargeKwh: 50, maxDischargeKwh: 50,
		socMinKwh: 0, socMaxKwh: 100, rte: 0.9,
	}
	hours := make([]optimumHour, 24)
	for i := range hours {
		hours[i] = optimumHour{tradable: true, importPrice: 5, exportPrice: 5, displaceableKwh: 100}
	}
	if got := optimizeDay(hours, 0, p, modeFull); got > 1e-6 {
		t.Fatalf("optimizeDay flat = %v, want ~0", got)
	}
}

// TestAggregateMonthOptimum exercises the full month path: with a cheap
// night hour and an expensive evening hour, a poorly-timed factual day
// should yield optimum ≫ fact, a positive reserve, and captured ≤ 1.
func TestAggregateMonthOptimum(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, loc)

	days := []DailyRecord{{
		Day:     day,
		IsFinal: true,
		Totals:  DailyTotals{EssNet: 10, EssDischarged: 40, HoursWithData: 24},
	}}

	hourly := make([]HourlyRecord, 0, 24)
	for h := 0; h < 24; h++ {
		hourly = append(hourly, HourlyRecord{HourStart: day.Add(time.Duration(h) * time.Hour)})
	}
	// Night: cheap, battery demonstrates a 40 kWh grid charge from empty.
	hourly[3] = HourlyRecord{
		HourStart: day.Add(3 * time.Hour), Rdn: floatPtr(1), ImportPrice: 1, ExportPrice: 1,
		GridToEss: 40, EssCharged: 40, EssRemainingKwhStart: floatPtr(0),
	}
	// Evening peak: expensive, demonstrates a 40 kWh discharge into load.
	hourly[19] = HourlyRecord{
		HourStart: day.Add(19 * time.Hour), Rdn: floatPtr(20), ImportPrice: 20, ExportPrice: 18,
		GridToLoad: 100, EssDischarged: 40, EssNet: 10, EssRemainingKwhStart: floatPtr(40),
	}

	got := AggregateMonth("2026-06", loc, days, hourly, 100, 0)

	// Optimum ≈ charge 40 @1 (−40) then discharge 40 to load @20 (+800) = 760.
	if got.Totals.EssOptimum < 700 {
		t.Fatalf("EssOptimum = %v, want ~760", got.Totals.EssOptimum)
	}
	if got.Totals.EssReserve <= 0 {
		t.Fatalf("EssReserve = %v, want > 0", got.Totals.EssReserve)
	}
	if math.Abs(got.Totals.EssReserve-(got.Totals.EssOptimum-got.Totals.EssFact)) > 1e-6 {
		t.Fatalf("EssReserve %v != optimum %v − fact %v", got.Totals.EssReserve, got.Totals.EssOptimum, got.Totals.EssFact)
	}
	// The three reasons must add up to the reserve.
	reasonsSum := got.Totals.EssReserveTiming + got.Totals.EssReserveSoc + got.Totals.EssReservePv
	if math.Abs(reasonsSum-got.Totals.EssReserve) > 1e-6 {
		t.Fatalf("reasons %v (t=%v s=%v p=%v) != reserve %v", reasonsSum,
			got.Totals.EssReserveTiming, got.Totals.EssReserveSoc, got.Totals.EssReservePv, got.Totals.EssReserve)
	}
	if got.Totals.EssCapturedShare <= 0 || got.Totals.EssCapturedShare > 1 {
		t.Fatalf("EssCapturedShare = %v, want (0,1]", got.Totals.EssCapturedShare)
	}
	if len(got.Days) != 1 || got.Days[0].EssOptimum < got.Days[0].Totals.EssNet {
		t.Fatalf("per-day optimum %+v must be ≥ fact", got.Days)
	}
}

// TestDeriveOptimumParams checks the empirical envelope: peak hourly
// charge/discharge as power, observed residual range as the SOC window,
// and discharged/charged as round-trip efficiency.
func TestDeriveOptimumParams(t *testing.T) {
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	hourly := []HourlyRecord{
		{HourStart: base, EssCharged: 30, EssDischarged: 0, EssRemainingKwhStart: floatPtr(20)},
		{HourStart: base.Add(time.Hour), EssCharged: 45, EssDischarged: 0, EssRemainingKwhStart: floatPtr(60)},
		{HourStart: base.Add(2 * time.Hour), EssCharged: 0, EssDischarged: 40, EssRemainingKwhStart: floatPtr(95)},
	}
	p := deriveOptimumParams(hourly, 200, 0.6)
	if p.maxChargeKwh != 45 {
		t.Fatalf("maxChargeKwh = %v, want 45", p.maxChargeKwh)
	}
	if p.maxDischargeKwh != 40 {
		t.Fatalf("maxDischargeKwh = %v, want 40", p.maxDischargeKwh)
	}
	if p.socMinKwh != 20 || p.socMaxKwh != 95 {
		t.Fatalf("SOC window = %v..%v, want 20..95", p.socMinKwh, p.socMaxKwh)
	}
	// rte = discharged/charged = 40/75 ≈ 0.533, clamped above 0.5.
	if math.Abs(p.rte-(40.0/75.0)) > 1e-9 {
		t.Fatalf("rte = %v, want %v", p.rte, 40.0/75.0)
	}
}
