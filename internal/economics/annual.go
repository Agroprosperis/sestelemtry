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
// and PV production (kWh) summed over the quarter's months. Quarter is
// 1..4 and Year identifies the calendar year so a sliding window that
// spans a year boundary labels its quarters unambiguously.
type QuarterSummary struct {
	Year      int
	Quarter   int
	EffectUah float64
	EbitdaUah float64
	PvKwh     float64
}

// MonthMargin is one row of the annual ESS marginality heatmap: 24
// hour-of-day cells, each summing that hour across every day of the
// month. nil entries are hours the pack sat out all month.
type MonthMargin struct {
	Month string // YYYY-MM
	Hours []*MarginHour
}

// StoredYear is the served annual economics result: the year rollup
// totals (same shape as a month), the 12 per-month rollups, the four
// quarter cards, and the month x hour-of-day marginality heatmap.
type StoredYear struct {
	OrganizationID string
	Period         string // YYYY (calendar year) or "YYYY-MM..YYYY-MM" (window)
	From           string // first month of the window, YYYY-MM
	To             string // last month of the window, YYYY-MM
	Tz             string
	Totals         MonthlyTotals
	Months         []MonthRollup
	Quarters       []QuarterSummary
	MonthlyMargin  []MonthMargin
	MonthsWithData int
	// PriorEbitda is the cumulative EBITDA of every stored day before the
	// window's first day — the ROI opening balance so a single-year view
	// still shows the CAPEX remaining since the start of operation.
	PriorEbitda float64
	// PriorMonthsWithData is the number of distinct months with data before
	// the window, so the frontend can annualise the all-time EBITDA over
	// the full operating span (window + prior) for the payback estimate.
	PriorMonthsWithData int
}

// AggregateYear folds a calendar year of persisted daily + hourly
// records into a year rollup. It buckets the records by calendar month,
// reuses AggregateMonth for each month, then sums the twelve
// MonthlyTotals into the year totals.
//
// ratingsFor returns the УЗЕ ratings effective on a given day and is
// handed straight to AggregateMonth, so a plant that grew mid-month is
// honored day by day.
func AggregateYear(
	period string,
	loc *time.Location,
	days []DailyRecord,
	hourly []HourlyRecord,
	ratingsFor func(day time.Time) EssRatings,
) StoredYear {
	year := parseYear(period)
	keys := make([]string, 0, 12)
	for m := 1; m <= 12; m++ {
		keys = append(keys, fmt.Sprintf("%04d-%02d", year, m))
	}
	return AggregatePeriod(period, keys, loc, days, hourly, ratingsFor)
}

// AggregatePeriod is the generalized rollup behind AggregateYear: it folds
// the given ordered list of YYYY-MM month keys (a calendar year, or an
// arbitrary sliding window that may cross a year boundary) into a single
// period rollup. Quarters are grouped by (calendar year, quarter) in the
// order they first appear, so a Jul→Jun window yields Q3, Q4, Q1, Q2 of
// the spanning years.
func AggregatePeriod(
	periodLabel string,
	monthKeys []string,
	loc *time.Location,
	days []DailyRecord,
	hourly []HourlyRecord,
	ratingsFor func(day time.Time) EssRatings,
) StoredYear {
	// Bucket the daily / hourly records by calendar month so each month
	// is aggregated against exactly its own slice.
	daysByMonth := make(map[string][]DailyRecord, len(monthKeys))
	hourlyByMonth := make(map[string][]HourlyRecord, len(monthKeys))
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
		months                 = make([]MonthRollup, 0, len(monthKeys))
		monthlyMargin          = make([]MonthMargin, 0, len(monthKeys))
		quarters               = make([]QuarterSummary, 0, 4)
		quarterIdx             = make(map[[2]int]int, 4)
		monthsWithData         int
	)

	for _, monthKey := range monthKeys {
		var ky, km int
		if _, err := fmt.Sscanf(monthKey, "%4d-%2d", &ky, &km); err != nil || km < 1 || km > 12 {
			continue
		}
		sm := AggregateMonth(monthKey, loc, daysByMonth[monthKey], hourlyByMonth[monthKey], ratingsFor)
		mt := sm.Totals
		months = append(months, MonthRollup{Month: monthKey, Totals: mt})
		monthlyMargin = append(monthlyMargin, MonthMargin{
			Month: monthKey,
			Hours: monthHourMargin(sm.HourlyMargin),
		})

		qkey := [2]int{ky, (km-1)/3 + 1}
		qi, ok := quarterIdx[qkey]
		if !ok {
			quarters = append(quarters, QuarterSummary{Year: ky, Quarter: qkey[1]})
			qi = len(quarters) - 1
			quarterIdx[qkey] = qi
		}
		quarters[qi].EffectUah += mt.Effect
		quarters[qi].EbitdaUah += mt.Ebitda
		quarters[qi].PvKwh += mt.PV

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

		// Fact-vs-optimum: each month already produced its continuous
		// monthly-DP headline, so the year is the sum of months.
		totals.EssFact += mt.EssFact
		totals.EssOptimum += mt.EssOptimum
		totals.EssReserveTiming += mt.EssReserveTiming
		totals.EssReserveSoc += mt.EssReserveSoc
		totals.EssReservePv += mt.EssReservePv
		totals.EssPvMissedKwh += mt.EssPvMissedKwh

		// Data quality: a period is OK only if every month is OK; sum the
		// excluded hours and concatenate civil dates that contained them.
		totals.EssDataQuality.TotalDays += mt.EssDataQuality.TotalDays
		totals.EssDataQuality.AnomalousHours += mt.EssDataQuality.AnomalousHours
		totals.EssDataQuality.AnomalousDays += mt.EssDataQuality.AnomalousDays
		totals.EssDataQuality.AnomalousDates = append(totals.EssDataQuality.AnomalousDates, mt.EssDataQuality.AnomalousDates...)
		totals.EssDataQuality.Anomalies = append(totals.EssDataQuality.Anomalies, mt.EssDataQuality.Anomalies...)
		if len(mt.EssDataQuality.ReasonCounts) > 0 {
			if totals.EssDataQuality.ReasonCounts == nil {
				totals.EssDataQuality.ReasonCounts = make(map[string]int)
			}
			for k, v := range mt.EssDataQuality.ReasonCounts {
				totals.EssDataQuality.ReasonCounts[k] += v
			}
		}
		if mt.EssDataQuality.MaxChargeKwhPerInterval > totals.EssDataQuality.MaxChargeKwhPerInterval {
			totals.EssDataQuality.MaxChargeKwhPerInterval = mt.EssDataQuality.MaxChargeKwhPerInterval
		}
		if mt.EssDataQuality.MaxDischargeKwhPerInterval > totals.EssDataQuality.MaxDischargeKwhPerInterval {
			totals.EssDataQuality.MaxDischargeKwhPerInterval = mt.EssDataQuality.MaxDischargeKwhPerInterval
		}
		if mt.EssDataQuality.MaxIntervalPowerKw > totals.EssDataQuality.MaxIntervalPowerKw {
			totals.EssDataQuality.MaxIntervalPowerKw = mt.EssDataQuality.MaxIntervalPowerKw
		}
		totals.EssDataQuality.PowerLimitKwhPerInterval = mt.EssDataQuality.PowerLimitKwhPerInterval

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
	totals.EssDataQuality.DataOK = totals.EssDataQuality.AnomalousHours == 0
	// ESS EOD snapshot: the last month with data carries the point-in-time
	// residual / cost-basis state.
	if lastMonthWithData != nil {
		totals.EssAvgCostBasisEod = lastMonthWithData.EssAvgCostBasisEod
		totals.EssResidualKwhEod = lastMonthWithData.EssResidualKwhEod
		totals.EssCostBasisUahEod = lastMonthWithData.EssCostBasisUahEod
	}

	from, to := "", ""
	if len(months) > 0 {
		from = months[0].Month
		to = months[len(months)-1].Month
	}
	return StoredYear{
		Period:         periodLabel,
		From:           from,
		To:             to,
		Tz:             loc.String(),
		Totals:         totals,
		Months:         months,
		Quarters:       quarters,
		MonthlyMargin:  monthlyMargin,
		MonthsWithData: monthsWithData,
	}
}

// monthHourMargin folds a month's daily heatmap rows into 24 hour-of-day
// cells: margin[h] = Σ realized profit at that hour / Σ kWh discharged at
// that hour. It reads the cells AggregateMonth already built rather than
// the raw hourly records, so the annual heatmap shows exactly the trades
// the monthly page shows — same anomaly filter, same minimum discharge,
// same cost basis — only summed over the month.
func monthHourMargin(days []DayMargin) []*MarginHour {
	out := make([]*MarginHour, 24)
	for _, d := range days {
		for h, c := range d.Hours {
			if c == nil || h < 0 || h >= 24 {
				continue
			}
			acc := out[h]
			if acc == nil {
				acc = &MarginHour{}
				out[h] = acc
			}
			acc.DischargedKwh += c.DischargedKwh
			acc.RevenueUah += c.RevenueUah
			acc.CostUah += c.CostUah
			acc.WearUah += c.WearUah
		}
	}
	for h, acc := range out {
		if acc == nil {
			continue
		}
		m := (acc.RevenueUah - acc.CostUah - acc.WearUah) / acc.DischargedKwh
		if acc.DischargedKwh <= 0 || math.IsInf(m, 0) || math.IsNaN(m) {
			out[h] = nil
			continue
		}
		acc.Margin = m
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

	result := AggregateYear(period, loc, daily, hourly, schedule.EssRatingsFor)
	result.OrganizationID = orgID
	if prior, priorMonths, perr := s.backend.SumEbitdaBefore(ctx, orgID, firstDay); perr == nil {
		result.PriorEbitda = prior
		result.PriorMonthsWithData = priorMonths
	}
	return result, nil
}

// maxWindowMonths bounds a sliding-period request so a malformed or
// abusive range can't pull an unbounded amount of history.
const maxWindowMonths = 36

// GetPeriod returns economics for an arbitrary inclusive month window
// [from..to] (both YYYY-MM in tz), reading the persisted daily/hourly
// records the same read-only way as GetYear. The window is clamped to
// maxWindowMonths and from/to are ordered if passed reversed.
func (s *Service) GetPeriod(ctx context.Context, orgID, from, to, tz string) (StoredYear, error) {
	loc, err := loadLocation(tz)
	if err != nil {
		return StoredYear{}, err
	}
	start, err := time.ParseInLocation("2006-01", from, loc)
	if err != nil {
		return StoredYear{}, fmt.Errorf("from must be YYYY-MM")
	}
	end, err := time.ParseInLocation("2006-01", to, loc)
	if err != nil {
		return StoredYear{}, fmt.Errorf("to must be YYYY-MM")
	}
	if end.Before(start) {
		start, end = end, start
	}

	// Build the ordered month keys, clamping the span to the cap.
	keys := make([]string, 0, 12)
	cur := start
	for !cur.After(end) && len(keys) < maxWindowMonths {
		keys = append(keys, cur.Format("2006-01"))
		cur = cur.AddDate(0, 1, 0)
	}

	firstDay := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, loc)
	lastKey, _ := time.ParseInLocation("2006-01", keys[len(keys)-1], loc)
	nextAfter := time.Date(lastKey.Year(), lastKey.Month(), 1, 0, 0, 0, 0, loc).AddDate(0, 1, 0)
	lastDay := nextAfter.AddDate(0, 0, -1)

	daily, err := s.backend.LoadDailyRange(ctx, orgID, firstDay, lastDay)
	if err != nil {
		return StoredYear{}, fmt.Errorf("load daily range: %w", err)
	}
	hourly, _ := s.backend.LoadHourlyRange(ctx, orgID, firstDay, nextAfter)

	schedule, _ := s.backend.TariffSchedule(ctx, orgID)

	label := keys[0] + ".." + keys[len(keys)-1]
	result := AggregatePeriod(label, keys, loc, daily, hourly, schedule.EssRatingsFor)
	result.OrganizationID = orgID
	if prior, priorMonths, perr := s.backend.SumEbitdaBefore(ctx, orgID, firstDay); perr == nil {
		result.PriorEbitda = prior
		result.PriorMonthsWithData = priorMonths
	}
	return result, nil
}
