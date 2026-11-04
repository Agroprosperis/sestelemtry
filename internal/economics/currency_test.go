package economics

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

// perDayScale converts each listed civil date (UTC) at its own factor;
// other dates stay as they are.
func perDayScale(factors map[string]float64) MoneyScale {
	return func(t time.Time) float64 {
		if f, ok := factors[t.UTC().Format("2006-01-02")]; ok {
			return f
		}
		return 1
	}
}

func savedDay(date string, ebitda float64) StoredDay {
	return StoredDay{
		Day:     mustDate(date),
		IsFinal: true,
		Totals: DailyTotals{
			Ebitda: ebitda, Effect: ebitda, RevenueTotal: ebitda, RevenuePvSelf: ebitda,
			PV: 10, Load: 10, PVToLoad: 10, AvgImportPrice: 4, HoursWithData: 24,
		},
	}
}

func sameMoney(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

// A month in a currency is the sum of its days, each at its own rate;
// energy stays as it is and the UAH service is untouched.
func TestInCurrencyConvertsEachDayAtItsOwnRate(t *testing.T) {
	fb := &fakeBackend{saved: map[string]StoredDay{
		"2026-06-01": savedDay("2026-06-01", 400),
		"2026-06-02": savedDay("2026-06-02", 1000),
	}}
	svc := NewService(fb)
	view := svc.InCurrency(perDayScale(map[string]float64{"2026-06-01": 1.0 / 40, "2026-06-02": 1.0 / 50}))

	got, err := view.GetMonthTotals(context.Background(), "org", "2026-06", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if !sameMoney(got.Ebitda, 400.0/40+1000.0/50) || got.PV != 20 {
		t.Errorf("view: ebitda %v pv %v, want 30 and 20", got.Ebitda, got.PV)
	}
	uah, err := svc.GetMonthTotals(context.Background(), "org", "2026-06", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if uah.Ebitda != 1400 {
		t.Errorf("UAH ebitda %v after a currency read, want 1400", uah.Ebitda)
	}
}

// The opening balance before a year follows the same rule as the year.
func TestInCurrencyOpensTheYearAtEachDaysRate(t *testing.T) {
	fb := &fakeBackend{saved: map[string]StoredDay{
		"2025-12-30": savedDay("2025-12-30", 400),
		"2025-12-31": savedDay("2025-12-31", 1000),
		"2026-01-05": savedDay("2026-01-05", 82),
	}}
	view := NewService(fb).InCurrency(perDayScale(map[string]float64{
		"2025-12-30": 1.0 / 40, "2025-12-31": 1.0 / 50, "2026-01-05": 1.0 / 41,
	}))
	got, err := view.GetYear(context.Background(), "org", "2026", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if !sameMoney(got.PriorEbitda, 30) || got.PriorMonthsWithData != 1 {
		t.Errorf("prior = %v over %d months, want 30 over 1", got.PriorEbitda, got.PriorMonthsWithData)
	}
	if !sameMoney(got.Totals.Ebitda, 2) {
		t.Errorf("year ebitda = %v, want 2", got.Totals.Ebitda)
	}
}

// A currency view reads stored days only: computing one from telemetry or
// writing one back is refused, so UAH stays the only stored currency.
func TestInCurrencyNeverComputesNorSaves(t *testing.T) {
	fb := &fakeBackend{}
	view := NewService(fb).InCurrency(perDayScale(nil))
	if _, err := view.ComputeDay(context.Background(), "org", "2026-06-03", "UTC"); err == nil {
		t.Error("a currency view computed a day")
	}
	if fb.saveCount != 0 || fb.flowsCalls != 0 {
		t.Errorf("a currency view saved %d days and read flows %d times", fb.saveCount, fb.flowsCalls)
	}
	cb := &currencyBackend{inner: fb, scale: perDayScale(nil)}
	if err := cb.SaveDay(context.Background(), StoredDay{}); !errors.Is(err, errCurrencyView) {
		t.Errorf("SaveDay = %v, want errCurrencyView", err)
	}
}

// The УЗЕ optimizer and the cycle threshold scale with the day's money:
// the same day stays a cycle, worth the UAH reserve at that day's rate,
// wear included.
func TestInCurrencyKeepsTheSameCycles(t *testing.T) {
	day := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	hourly := make([]HourlyRecord, 24)
	for h := range hourly {
		hourly[h] = HourlyRecord{HourStart: day.Add(time.Duration(h) * time.Hour)}
	}
	hourly[3] = HourlyRecord{
		HourStart: day.Add(3 * time.Hour), Rdn: floatPtr(1), ImportPrice: 1, ExportPrice: 1,
		GridToEss: 40, EssCharged: 40, EssRemainingKwhStart: floatPtr(0),
	}
	hourly[19] = HourlyRecord{
		HourStart: day.Add(19 * time.Hour), Rdn: floatPtr(40), ImportPrice: 40, ExportPrice: 38,
		GridToLoad: 100, EssDischarged: 40, EssNet: 10, EssRemainingKwhStart: floatPtr(40),
	}
	fb := &fakeBackend{
		hourly: hourly,
		saved: map[string]StoredDay{"2026-06-01": {
			Day: day, IsFinal: true,
			Totals: DailyTotals{EssNet: 10, EssDischarged: 40, HoursWithData: 24},
		}},
		schedule: Schedule{{
			EffectiveFrom: mustDate("1970-01-01"),
			Tariffs:       Tariffs{EssCapacityKwh: 100, DegradationUahPerKwh: 2},
		}},
	}
	const rate = 40.0
	uah, err := NewService(fb).GetMonth(context.Background(), "org", "2026-06", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	usd, err := NewService(fb).InCurrency(perDayScale(map[string]float64{"2026-06-01": 1 / rate})).
		GetMonth(context.Background(), "org", "2026-06", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if len(uah.Cycles) != 1 || len(usd.Cycles) != 1 {
		t.Fatalf("cycles: UAH %d, currency %d; want 1 and 1", len(uah.Cycles), len(usd.Cycles))
	}
	if usd.Cycles[0].ReserveUah >= cycleReserveThresholdUah {
		t.Fatalf("currency reserve %v is not converted", usd.Cycles[0].ReserveUah)
	}
	if !sameMoney(usd.Cycles[0].ReserveUah*rate, uah.Cycles[0].ReserveUah) || !sameMoney(usd.Cycles[0].OptEffectUah*rate, uah.Cycles[0].OptEffectUah) {
		t.Errorf("currency cycle %+v × %v != UAH cycle %+v", usd.Cycles[0], rate, uah.Cycles[0])
	}
}

// ScaleDay converts money and prices, leaves energy alone and does not
// touch the day it was given.
func TestScaleDayConvertsMoneyNotEnergy(t *testing.T) {
	in := StoredDay{
		Rows: []*HourRow{nil, {
			Rdn:                  ptr(4),
			Flow:                 HourFlows{PV: 10},
			Econ:                 HourEconomics{Load: 8, ImportPrice: 5, ExportPrice: 3, Effect: 40},
			EssRemainingKwhStart: ptr(50),
			EssCostBasisUahStart: ptr(100),
		}},
		Totals: DailyTotals{Ebitda: 80, PV: 10, AvgImportPrice: 5},
	}
	out := ScaleDay(in, 0.5)
	r := out.Rows[1]
	if out.Rows[0] != nil || *r.Rdn != 2 || r.Econ.ImportPrice != 2.5 || r.Econ.Effect != 20 || *r.EssCostBasisUahStart != 50 {
		t.Errorf("money not halved: %+v", r)
	}
	if r.Flow.PV != 10 || r.Econ.Load != 8 || *r.EssRemainingKwhStart != 50 {
		t.Errorf("energy changed: %+v", r)
	}
	if out.Totals.Ebitda != 40 || out.Totals.AvgImportPrice != 2.5 || out.Totals.PV != 10 {
		t.Errorf("totals = %+v", out.Totals)
	}
	if *in.Rows[1].Rdn != 4 || in.Rows[1].Econ.Effect != 40 || in.Totals.Ebitda != 80 {
		t.Error("ScaleDay changed its input")
	}
}
