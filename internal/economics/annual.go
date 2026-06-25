package economics

import (
	"context"
	"fmt"
	"math"
	"time"
)

// MonthRollup is one month's contribution to the annual view. Totals
// reuses the monthly rollup shape verbatim so the frontend renders the
// per-month trend / table from the same fields the monthly dashboard
// already knows.
type MonthRollup struct {
	Month  string // YYYY-MM
	Totals MonthlyTotals
}

// QuarterSummary is one quarter card: the project effect, EBITDA (UAH)
// and PV production (kWh) summed over the quarter's months. Quarter is 1..4.
type QuarterSummary struct {
	Quarter   int
	EffectUah float64
	EbitdaUah float64
	PvKwh     float64
}

// MonthMargin is one row of the annual ESS marginality heatmap: 24
// hourly margins (UAH per kWh discharged) averaged across the month.
// nil entries are hours with no discharge that month.
type MonthMargin struct {
	Month string // YYYY-MM
	Hours []*float64
}

// StoredYear is the served annual economics result: the year rollup
// totals (same shape as a month), the 12 per-month rollups, the four
// quarter cards, and the month x hour-of-day marginality heatmap.
type StoredYear struct {
	OrganizationID string
	Period         string // YYYY
	Tz             string
	Totals         MonthlyTotals
	Months         []MonthRollup
	Quarters       []QuarterSummary
	MonthlyMargin  []MonthMargin
	MonthsWithData int
}

// AggregateYear folds a calendar year of persisted daily + hourly
// records into a year rollup. It buckets the records by calendar month,
// reuses AggregateMonth for each month (resolving the ESS capacity /
// degradation per month via resolveTariff so a mid-year tariff change is
// honored), then sums the twelve MonthlyTotals into the year totals.
//
// resolveTariff returns the usable ESS capacity (kWh) and degradation
// cost (UAH/kWh) effective on the given day; AggregateYear calls it with
// the first day of each month.
func AggregateYear(
	period string,
	loc *time.Location,
	days []DailyRecord,
	hourly []HourlyRecord,
	resolveTariff func(day time.Time) (capacityKwh, degradationUahPerKwh float64),
) StoredYear {
	year := parseYear(period)

	// Bucket the daily / hourly records by calendar month so each month
	// is aggregated against exactly its own slice.
	daysByMonth := make(map[string][]DailyRecord, 12)
	hourlyByMonth := make(map[string][]HourlyRecord, 12)
	for _, d := range days {
		k := d.Day.In(loc).Format("2006-01")
		daysByMonth[k] = append(daysByMonth[k], d)
	}
	for _, h := range hourly {
		k := h.HourStart.In(loc).Format("2006-01")
		hourlyByMonth[k] = append(hourlyByMonth[k], h)
	}

	var (
		totals                 MonthlyTotals
		importNum, importDen   float64
		exportNum, exportDen   float64
		rdnNum, rdnDen, rdnMax float64
		haveRdnMax             bool
		bestSet, minSet        bool
		lastMonthWithData      *MonthlyTotals
		months                 = make([]MonthRollup, 0, 12)
		monthlyMargin          = make([]MonthMargin, 0, 12)
		quarters               = [4]QuarterSummary{{Quarter: 1}, {Quarter: 2}, {Quarter: 3}, {Quarter: 4}}
		monthsWithData         int
	)

	for m := 1; m <= 12; m++ {
		monthKey := fmt.Sprintf("%04d-%02d", year, m)
		firstDay := time.Date(year, time.Month(m), 1, 0, 0, 0, 0, loc)
		capacityKwh, degr := resolveTariff(firstDay)

		sm := AggregateMonth(monthKey, loc, daysByMonth[monthKey], hourlyByMonth[monthKey], capacityKwh, degr)
		mt := sm.Totals
		months = append(months, MonthRollup{Month: monthKey, Totals: mt})
		monthlyMargin = append(monthlyMargin, MonthMargin{
			Month: monthKey,
			Hours: monthHourMargin(hourlyByMonth[monthKey], loc),
		})

		q := &quarters[(m-1)/3]
		q.EffectUah += mt.Effect
		q.EbitdaUah += mt.Ebitda
		q.PvKwh += mt.PV

		if mt.HoursWithData == 0 {
			continue
		}
		monthsWithData++

		// Additive fields: plain sums of the month rollups.
		totals.BaselineCost += mt.BaselineCost
		totals.ActualCost += mt.ActualCost
		totals.Effect += mt.Effect
		totals.EssNet += mt.EssNet
		totals.Load += mt.Load
		totals.PV += mt.PV
		totals.GridImport += mt.GridImport
		totals.GridExport += mt.GridExport
		totals.EssCharged += mt.EssCharged
		totals.EssDischarged += mt.EssDischarged
		totals.PVToLoad += mt.PVToLoad
		totals.PVToEss += mt.PVToEss
		totals.PVToGrid += mt.PVToGrid
		totals.GridToLoad += mt.GridToLoad
		totals.GridToEss += mt.GridToEss
		totals.EssToLoad += mt.EssToLoad
		totals.EssToGrid += mt.EssToGrid
		totals.RevenuePvExport += mt.RevenuePvExport
		totals.RevenuePvSelf += mt.RevenuePvSelf
		totals.RevenueEssExport += mt.RevenueEssExport
		totals.RevenueEssSelf += mt.RevenueEssSelf
		totals.ExpenseGridCharge += mt.ExpenseGridCharge
		totals.EssWithdrawnCost += mt.EssWithdrawnCost
		totals.EssRealizedProfit += mt.EssRealizedProfit
		totals.EssDegradationCost += mt.EssDegradationCost
		totals.HoursWithData += mt.HoursWithData
		totals.HoursMissingPrice += mt.HoursMissingPrice
		totals.DaysWithData += mt.DaysWithData
		// Equivalent cycles are summed (each month uses its own capacity,
		// so a single year-level division by one capacity would be wrong
		// when the pack size changes mid-year).
		totals.EquivalentCycles += mt.EquivalentCycles

		// Fact-vs-optimum: each month already summed its per-day ladder,
		// so the year is the sum of months.
		totals.EssFact += mt.EssFact
		totals.EssOptimum += mt.EssOptimum
		totals.EssReserveTiming += mt.EssReserveTiming
		totals.EssReserveSoc += mt.EssReserveSoc
		totals.EssReservePv += mt.EssReservePv
		totals.EssPvMissedKwh += mt.EssPvMissedKwh

		// kWh-weighted price reconstruction (same scheme AggregateMonth
		// uses to roll days up — multiply the month's weighted average
		// back by its kWh to recover the exact numerator).
		importNum += mt.AvgImportPrice * mt.GridImport
		importDen += mt.GridImport
		exportNum += mt.AvgExportPrice * mt.GridExport
		exportDen += mt.GridExport
		rdnNum += mt.RdnAvgUahPerKwh * mt.GridImport
		rdnDen += mt.GridImport
		if mt.RdnMaxUahPerKwh > 0 && (!haveRdnMax || mt.RdnMaxUahPerKwh > rdnMax) {
			rdnMax = mt.RdnMaxUahPerKwh
			haveRdnMax = true
		}

		// Best / worst day of the year: pick the extreme across the
		// months' own extremes.
		if mt.BestDay.Date != "" && (!bestSet || mt.BestDay.EffectUah > totals.BestDay.EffectUah) {
			totals.BestDay = mt.BestDay
			bestSet = true
		}
		if mt.MinEffectDay.Date != "" && (!minSet || mt.MinEffectDay.EffectUah < totals.MinEffectDay.EffectUah) {
			totals.MinEffectDay = mt.MinEffectDay
			minSet = true
		}

		mtCopy := mt
		lastMonthWithData = &mtCopy
	}

	if importDen > 0 {
		totals.AvgImportPrice = importNum / importDen
	}
	if exportDen > 0 {
		totals.AvgExportPrice = exportNum / exportDen
	}
	if rdnDen > 0 {
		totals.RdnAvgUahPerKwh = rdnNum / rdnDen
	}
	totals.RdnMaxUahPerKwh = rdnMax
	totals.RevenueTotal = totals.RevenuePvExport + totals.RevenuePvSelf + totals.RevenueEssExport + totals.RevenueEssSelf
	totals.ExpenseTotal = totals.ExpenseGridCharge
	totals.Ebitda = totals.RevenueTotal - totals.ExpenseTotal
	totals.EssReserve = totals.EssOptimum - totals.EssFact
	if totals.EssOptimum > 0 {
		totals.EssCapturedShare = totals.EssFact / totals.EssOptimum
	}
	// ESS EOD snapshot: the last month with data carries the point-in-time
	// residual / cost-basis state.
	if lastMonthWithData != nil {
		totals.EssAvgCostBasisEod = lastMonthWithData.EssAvgCostBasisEod
		totals.EssResidualKwhEod = lastMonthWithData.EssResidualKwhEod
		totals.EssCostBasisUahEod = lastMonthWithData.EssCostBasisUahEod
	}

	return StoredYear{
		Period:         period,
		Tz:             loc.String(),
		Totals:         totals,
		Months:         months,
		Quarters:       quarters[:],
		MonthlyMargin:  monthlyMargin,
		MonthsWithData: monthsWithData,
	}
}

// monthHourMargin reduces a month's hourly records into 24 hour-of-day
// average discharge margins (UAH per kWh), aggregated across every day
// of the month: margin[h] = Σ ess_net(h) / Σ ess_discharged(h). Hours
// with no discharge stay nil so the heatmap renders an empty cell.
func monthHourMargin(hourly []HourlyRecord, loc *time.Location) []*float64 {
	var net, dis [24]float64
	for _, h := range hourly {
		hour := h.HourStart.In(loc).Hour()
		if hour < 0 || hour >= 24 {
			continue
		}
		net[hour] += h.EssNet
		dis[hour] += h.EssDischarged
	}
	out := make([]*float64, 24)
	for h := 0; h < 24; h++ {
		if dis[h] > 0 {
			m := net[h] / dis[h]
			if !math.IsInf(m, 0) && !math.IsNaN(m) {
				v := m
				out[h] = &v
			}
		}
	}
	return out
}

// parseYear extracts the 4-digit year from a YYYY period string,
// falling back to the current year on a malformed input (the handler
// validates the format before calling, so this is a belt-and-braces
// guard rather than a user-facing error path).
func parseYear(period string) int {
	var y int
	if _, err := fmt.Sscanf(period, "%4d", &y); err != nil || y < 1970 || y > 9999 {
		return time.Now().Year()
	}
	return y
}

// GetYear returns economics for one calendar year as a pure read of the
// persisted daily/hourly records (the economics-recompute daemon keeps
// them warm). period is YYYY in tz.
func (s *Service) GetYear(ctx context.Context, orgID, period, tz string) (StoredYear, error) {
	loc, err := loadLocation(tz)
	if err != nil {
		return StoredYear{}, err
	}
	parsed, err := time.ParseInLocation("2006", period, loc)
	if err != nil {
		return StoredYear{}, fmt.Errorf("period must be YYYY")
	}
	year := parsed.Year()
	firstDay := time.Date(year, 1, 1, 0, 0, 0, 0, loc)
	nextYear := firstDay.AddDate(1, 0, 0)
	lastDay := nextYear.AddDate(0, 0, -1)

	daily, err := s.backend.LoadDailyRange(ctx, orgID, firstDay, lastDay)
	if err != nil {
		return StoredYear{}, fmt.Errorf("load daily range: %w", err)
	}
	hourly, _ := s.backend.LoadHourlyRange(ctx, orgID, firstDay, nextYear)

	schedule, _ := s.backend.TariffSchedule(ctx, orgID)
	resolve := func(day time.Time) (float64, float64) {
		t, ok := schedule.ResolveForDay(day)
		if !ok {
			t = DefaultTariffs
		}
		return t.EssCapacityKwh, t.DegradationUahPerKwh
	}

	result := AggregateYear(period, loc, daily, hourly, resolve)
	result.OrganizationID = orgID
	return result, nil
}
