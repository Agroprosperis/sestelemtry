package economics

import (
	"math"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// synthPeriod builds a realistic window of persisted daily + hourly
// records — PV bell curve, midday PV charge, night grid charge, evening
// discharge, RDN day shape — so the rollups run every optimizer path the
// way production data does.
func synthPeriod(loc *time.Location, from time.Time, months int) ([]DailyRecord, []HourlyRecord, []string) {
	rng := rand.New(rand.NewSource(42))
	var days []DailyRecord
	var hourly []HourlyRecord
	var keys []string
	start := time.Date(from.Year(), from.Month(), 1, 0, 0, 0, 0, loc)
	end := start.AddDate(0, months, 0)
	for m := start; m.Before(end); m = m.AddDate(0, 1, 0) {
		keys = append(keys, m.Format("2006-01"))
	}
	soc := 100.0
	for d := start; d.Before(end); d = d.AddDate(0, 0, 1) {
		var t DailyTotals
		season := 0.5 + 0.5*math.Sin(float64(d.YearDay())/365*2*math.Pi-math.Pi/2)
		for h := 0; h < 24; h++ {
			hs := time.Date(d.Year(), d.Month(), d.Day(), h, 0, 0, 0, loc)
			rdn := 3 + 3*math.Sin(float64(h-6)/24*2*math.Pi) + rng.Float64()
			imp := rdn + 3.5
			exp := rdn * 0.95
			pv := 0.0
			if h >= 6 && h <= 19 {
				pv = 450 * season * math.Sin(float64(h-6)/13*math.Pi) * (0.7 + 0.3*rng.Float64())
			}
			load := 150 + 50*rng.Float64()
			var chg, dis, pvToEss, gridToEss, essToLoad float64
			if h >= 11 && h <= 13 && pv > 0 {
				chg = math.Min(math.Min(pv*0.5, 200), 516-soc)
				pvToEss = chg
			}
			if h == 3 {
				gridToEss = math.Min(120, 516-soc)
				chg += gridToEss
			}
			if h >= 18 && h <= 21 {
				dis = math.Min(180, soc)
				essToLoad = math.Min(dis, load)
			}
			socStart := soc
			soc = math.Max(0, math.Min(516, soc+chg*0.95-dis/0.95))
			pvLeft := math.Max(0, pv-pvToEss)
			pvToLoad := math.Min(pvLeft, load)
			pvToGrid := pvLeft - pvToLoad
			gridToLoad := math.Max(0, load-pvToLoad-essToLoad)
			essToGrid := dis - essToLoad
			rdnCopy := rdn
			essNet := essToLoad*imp + essToGrid*exp - pvToEss*exp - gridToEss*imp - dis*0.6
			wc := dis * 4
			rp := essToLoad*imp + essToGrid*exp - wc - dis*0.6
			hourly = append(hourly, HourlyRecord{
				HourStart: hs, Rdn: &rdnCopy, ImportPrice: imp, ExportPrice: exp,
				GridImport: gridToLoad + gridToEss, PV: pv,
				PVToLoad: pvToLoad, PVToGrid: pvToGrid, PVToEss: pvToEss,
				GridToEss: gridToEss, GridToLoad: gridToLoad,
				EssToLoad: essToLoad, EssToGrid: essToGrid,
				EssCharged: chg, EssDischarged: dis, EssNet: essNet,
				EssWithdrawnCostUah: &wc, EssRealizedProfitUah: &rp,
				EssRemainingKwhStart: &socStart,
				EssPeakIntervalKw:    math.Max(chg, dis) * 1.1,
			})
			t.PV += pv
			t.Load += load
			t.GridImport += gridToLoad + gridToEss
			t.GridExport += pvToGrid + essToGrid
			t.EssCharged += chg
			t.EssDischarged += dis
			t.PVToLoad += pvToLoad
			t.PVToEss += pvToEss
			t.PVToGrid += pvToGrid
			t.GridToLoad += gridToLoad
			t.GridToEss += gridToEss
			t.EssToLoad += essToLoad
			t.EssToGrid += essToGrid
			t.EssNet += essNet
			t.RevenuePvSelf += pvToLoad * imp
			t.RevenuePvExport += pvToGrid * exp
			t.RevenueEssSelf += essToLoad * imp
			t.ExpenseGridCharge += gridToEss * imp
			t.HoursWithData++
		}
		t.AvgImportPrice = 7
		t.AvgExportPrice = 3
		t.RevenueTotal = t.RevenuePvSelf + t.RevenuePvExport + t.RevenueEssSelf
		t.ExpenseTotal = t.ExpenseGridCharge
		t.Ebitda = t.RevenueTotal - t.ExpenseTotal
		days = append(days, DailyRecord{Day: d, Totals: t, IsFinal: true})
	}
	return days, hourly, keys
}

// synthRatings is a 516 kWh usable pack; powerKw 0 falls back to ≈1C
// (the widest DP moves) and 120 kW flags the synthetic 200 kWh midday
// charge as anomalous, exercising the filter.
func synthRatings(powerKw float64) func(time.Time) EssRatings {
	return fixedRatings(516, 0.6, powerKw, 0.9)
}

func splitByMonth(loc *time.Location, days []DailyRecord, hourly []HourlyRecord) (map[string][]DailyRecord, map[string][]HourlyRecord) {
	dm := map[string][]DailyRecord{}
	hm := map[string][]HourlyRecord{}
	for _, d := range days {
		k := d.Day.In(loc).Format("2006-01")
		dm[k] = append(dm[k], d)
	}
	for _, h := range hourly {
		k := h.HourStart.In(loc).Format("2006-01")
		hm[k] = append(hm[k], h)
	}
	return dm, hm
}

// TestAggregateMonthTotalsMatchesAggregateMonth pins that the totals-only
// rollup the multi-month views use yields exactly the month page's
// totals and heatmap — skipping the per-day ladder and cycles must not
// move a single number.
func TestAggregateMonthTotalsMatchesAggregateMonth(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	days, hourly, _ := synthPeriod(loc, time.Date(2025, 7, 1, 0, 0, 0, 0, loc), 1)
	for _, power := range []float64{250, 0, 120} {
		full := AggregateMonth("2025-07", loc, days, hourly, synthRatings(power))
		lean := aggregateMonthTotals("2025-07", loc, days, hourly, synthRatings(power))
		if len(full.Cycles) == 0 {
			t.Fatalf("power %v: synthetic month produced no cycles; the comparison would not cover the per-day DP", power)
		}
		if !reflect.DeepEqual(full.Totals, lean.Totals) {
			t.Errorf("power %v: totals differ:\nfull %+v\nlean %+v", power, full.Totals, lean.Totals)
		}
		if !reflect.DeepEqual(full.HourlyMargin, lean.HourlyMargin) {
			t.Errorf("power %v: heatmap differs", power)
		}
		if lean.DaysInMonth != full.DaysInMonth || lean.Days != nil || lean.Cycles != nil {
			t.Errorf("power %v: lean rollup DaysInMonth=%d days=%d cycles=%d, want %d/nil/nil",
				power, lean.DaysInMonth, len(lean.Days), len(lean.Cycles), full.DaysInMonth)
		}
	}
}

// TestAggregatePeriodMatchesMonthRollups checks the concurrent period
// rollup against month-by-month AggregateMonth: every month in calendar
// order with identical totals.
func TestAggregatePeriodMatchesMonthRollups(t *testing.T) {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	days, hourly, keys := synthPeriod(loc, time.Date(2025, 1, 1, 0, 0, 0, 0, loc), 14)
	dm, hm := splitByMonth(loc, days, hourly)
	got := AggregatePeriod("x", keys, loc, days, hourly, synthRatings(120))
	if len(got.Months) != len(keys) {
		t.Fatalf("months = %d, want %d", len(got.Months), len(keys))
	}
	var ebitda float64
	for i, k := range keys {
		want := AggregateMonth(k, loc, dm[k], hm[k], synthRatings(120)).Totals
		if got.Months[i].Month != k || !reflect.DeepEqual(got.Months[i].Totals, want) {
			t.Fatalf("month %d = %s, totals differ from AggregateMonth(%s)", i, got.Months[i].Month, k)
		}
		ebitda += want.Ebitda
	}
	if math.Abs(got.Totals.Ebitda-ebitda) > 1e-6 {
		t.Fatalf("period EBITDA = %v, want %v", got.Totals.Ebitda, ebitda)
	}
}

// BenchmarkAggregatePeriod rolls up 26 months — the payback page's
// all-time window — for one object.
func BenchmarkAggregatePeriod(b *testing.B) {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		b.Skip("tzdata unavailable")
	}
	days, hourly, keys := synthPeriod(loc, time.Date(2024, 8, 1, 0, 0, 0, 0, loc), 26)
	for b.Loop() {
		AggregatePeriod("x", keys, loc, days, hourly, synthRatings(250))
	}
}
