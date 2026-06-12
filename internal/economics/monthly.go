package economics

import (
	"context"
	"fmt"
	"math"
	"time"
)

// DailyRecord is a persisted per-day economics summary as seen by the
// monthly aggregator. It carries the daily totals plus the finality flag
// so GetMonth can trust final days from cache and recompute the rest.
type DailyRecord struct {
	Day        time.Time
	Totals     DailyTotals
	IsFinal    bool
	ComputedAt time.Time
}

// HourlyRecord is the per-hour slice the monthly aggregator needs for the
// RDN price stats, the ESS marginality heatmap, and the fact-vs-optimum
// optimizer (prices, PV surplus, displaceable load, SOC residual).
type HourlyRecord struct {
	HourStart   time.Time
	Rdn         *float64
	ImportPrice float64
	ExportPrice float64
	GridImport  float64

	PVToLoad   float64
	PVToGrid   float64
	PVToEss    float64
	GridToEss  float64
	GridToLoad float64
	EssToLoad  float64

	EssCharged    float64
	EssDischarged float64
	EssNet        float64

	EssRemainingKwhStart *float64
}

// MonthDay is one day's contribution to the month — the full daily
// totals plus a couple of per-day derived values the dashboard renders
// in the daily-detail table and the trend chart.
type MonthDay struct {
	Date             string
	Totals           DailyTotals
	RdnAvgUahPerKwh  float64
	EquivalentCycles float64
	IsFinal          bool

	// Fact-vs-optimum (all on the PV-free basis, per the example: stored
	// PV has cost 0). EssFact is the realised effect on that basis;
	// EssOptimum the modelled maximum; EssReserve = EssOptimum − EssFact,
	// decomposed into the timing / pre-peak-SOC / missed-PV causes.
	EssFact          float64
	EssOptimum       float64
	EssReserve       float64
	EssReserveTiming float64
	EssReserveSoc    float64
	EssReservePv     float64
	EssPvMissedKwh   float64
}

// DayMargin is one row of the ESS marginality heatmap: 24 hourly margins
// (UAH per kWh discharged) for one civil day. nil entries are hours with
// no discharge (or no price).
type DayMargin struct {
	Date  string
	Hours []*float64
}

// MonthExtreme records the day with the best / worst project effect.
type MonthExtreme struct {
	Date      string
	EffectUah float64
}

// MonthlyTotals is the month rollup. Additive fields are plain sums of
// the daily totals; AvgImport/ExportPrice and RdnAvg are kWh-weighted;
// the ESS EOD snapshot fields are taken from the last day with data (a
// point-in-time state, not a sum).
type MonthlyTotals struct {
	BaselineCost float64
	ActualCost   float64
	Effect       float64
	EssNet       float64

	Load          float64
	PV            float64
	GridImport    float64
	GridExport    float64
	EssCharged    float64
	EssDischarged float64
	PVToLoad      float64
	PVToEss       float64
	PVToGrid      float64
	GridToLoad    float64
	GridToEss     float64
	EssToLoad     float64
	EssToGrid     float64

	AvgImportPrice  float64
	AvgExportPrice  float64
	RdnAvgUahPerKwh float64
	RdnMaxUahPerKwh float64

	RevenuePvExport   float64
	RevenuePvSelf     float64
	RevenueEssExport  float64
	RevenueEssSelf    float64
	RevenueTotal      float64
	ExpenseGridCharge float64
	ExpenseTotal      float64
	Ebitda            float64

	EssWithdrawnCost   float64
	EssRealizedProfit  float64
	EssDegradationCost float64
	EssAvgCostBasisEod float64
	EssResidualKwhEod  float64
	EssCostBasisUahEod float64

	EquivalentCycles  float64
	DaysWithData      int
	HoursWithData     int
	HoursMissingPrice int

	// ESS fact-vs-optimum rollup (PV-free basis). EssFact is the realised
	// effect, EssOptimum the sum of per-day modelled maxima (each clamped
	// ≥ that day's fact, so EssReserve ≥ 0). EssCapturedShare =
	// EssFact / EssOptimum. The reserve is split into three causes that
	// sum back to EssReserve.
	EssFact          float64
	EssOptimum       float64
	EssReserve       float64
	EssCapturedShare float64
	EssReserveTiming float64
	EssReserveSoc    float64
	EssReservePv     float64
	EssPvMissedKwh   float64

	BestDay      MonthExtreme
	MinEffectDay MonthExtreme
}

// StoredMonth is the served monthly economics result: the rollup totals,
// the per-day breakdown, and the hourly ESS marginality grid.
type StoredMonth struct {
	OrganizationID string
	Month          string // YYYY-MM
	Tz             string
	Totals         MonthlyTotals
	Days           []MonthDay
	HourlyMargin   []DayMargin
	DaysInMonth    int
}

// AggregateMonth folds the persisted daily + hourly records of one month
// into a MonthlyTotals + per-day breakdown + ESS marginality heatmap.
// capacityKwh is the ESS capacity used for the equivalent-cycle metric.
func AggregateMonth(month string, loc *time.Location, days []DailyRecord, hourly []HourlyRecord, capacityKwh, degradationUahPerKwh float64) StoredMonth {
	// Per-day RDN stats keyed by civil date, derived from the hourly
	// slice (the daily table stores only the all-in import price).
	type rdnAcc struct {
		num float64 // sum(rdn * import)
		den float64 // sum(import)
	}
	// Per-day optimizer context: the 24 hourly slots, the residual at the
	// earliest hour (the day's starting SOC), and the accumulators the
	// fact-vs-optimum decomposition needs.
	type dayOpt struct {
		hours       [24]optimumHour
		startKwh    float64
		startHour   int
		haveStart   bool
		pvChargeOpp float64 // Σ pv_to_ess × export (PV→free basis adjustment)
		pvSurplus   float64 // Σ available PV surplus
		actualPv    float64 // Σ actual pv_to_ess
	}
	dayRdn := make(map[string]*rdnAcc)
	dayOpts := make(map[string]*dayOpt)
	margins := make(map[string][]*float64)
	var monthRdnNum, monthRdnDen, monthRdnMax float64
	haveRdnMax := false

	for _, h := range hourly {
		local := h.HourStart.In(loc)
		key := local.Format("2006-01-02")
		hour := local.Hour()
		grid := margins[key]
		if grid == nil {
			grid = make([]*float64, 24)
			margins[key] = grid
		}
		if hour >= 0 && hour < 24 && h.EssDischarged > 0 {
			m := h.EssNet / h.EssDischarged
			if !math.IsInf(m, 0) && !math.IsNaN(m) {
				v := m
				grid[hour] = &v
			}
		}
		if hour >= 0 && hour < 24 {
			do := dayOpts[key]
			if do == nil {
				do = &dayOpt{}
				dayOpts[key] = do
			}
			pvSurplus := h.PVToGrid + h.PVToEss
			oh := optimumHour{
				// PV not consumed by load is available to charge (or
				// export); load not served by PV is the import the
				// battery can displace at import price.
				pvSurplusKwh:        pvSurplus,
				displaceableKwh:     h.GridToLoad + h.EssToLoad,
				actualPvChargeKwh:   h.PVToEss,
				actualGridChargeKwh: h.GridToEss,
			}
			if h.Rdn != nil {
				oh.tradable = true
				oh.importPrice = h.ImportPrice
				oh.exportPrice = h.ExportPrice
				do.pvChargeOpp += h.PVToEss * h.ExportPrice
			}
			do.hours[hour] = oh
			do.pvSurplus += pvSurplus
			do.actualPv += h.PVToEss
			if h.EssRemainingKwhStart != nil && (!do.haveStart || hour < do.startHour) {
				do.startKwh = *h.EssRemainingKwhStart
				do.startHour = hour
				do.haveStart = true
			}
		}
		if h.Rdn != nil {
			acc := dayRdn[key]
			if acc == nil {
				acc = &rdnAcc{}
				dayRdn[key] = acc
			}
			acc.num += *h.Rdn * h.GridImport
			acc.den += h.GridImport
			monthRdnNum += *h.Rdn * h.GridImport
			monthRdnDen += h.GridImport
			if !haveRdnMax || *h.Rdn > monthRdnMax {
				monthRdnMax = *h.Rdn
				haveRdnMax = true
			}
		}
	}

	// Fact-vs-optimum: derive the battery envelope from this month's
	// hourly history, then solve each day's optimal dispatch on the
	// PV-free basis and attribute the reserve to its causes.
	type optimumResult struct {
		fact     float64 // realised effect on the PV-free basis
		optimum  float64
		reserve  float64
		timing   float64 // discharge not at peak
		soc      float64 // pre-peak SOC / grid charge timing
		pv       float64 // missed PV charging
		pvMissed float64 // available PV surplus that was not stored (kWh)
	}
	optParams := deriveOptimumParams(hourly, capacityKwh, degradationUahPerKwh)

	// computeOptimum solves the 3-run ladder for one day: each relaxation
	// only adds freedom, so the values are monotonic and the three
	// reasons are non-negative and sum to the reserve.
	computeOptimum := func(do *dayOpt, factPv0 float64) optimumResult {
		if do == nil {
			return optimumResult{fact: factPv0, optimum: factPv0}
		}
		start := optParams.socMinKwh
		if do.haveStart {
			start = do.startKwh
		}
		fixed := optimizeDay(do.hours[:], start, optParams, modeFixedCharge)
		noPv := optimizeDay(do.hours[:], start, optParams, modeNoPV)
		full := optimizeDay(do.hours[:], start, optParams, modeFull)
		// Clamp into a monotonic ladder bottomed at the fact.
		if fixed < factPv0 {
			fixed = factPv0
		}
		if noPv < fixed {
			noPv = fixed
		}
		if full < noPv {
			full = noPv
		}
		return optimumResult{
			fact:     factPv0,
			optimum:  full,
			reserve:  full - factPv0,
			timing:   fixed - factPv0,
			soc:      noPv - fixed,
			pv:       full - noPv,
			pvMissed: math.Max(0, do.pvSurplus-do.actualPv),
		}
	}

	var totals MonthlyTotals
	var importNum, importDen, exportNum, exportDen float64
	var bestSet, minSet bool
	outDays := make([]MonthDay, 0, len(days))

	for _, d := range days {
		t := d.Totals
		key := d.Day.In(loc).Format("2006-01-02")

		totals.BaselineCost += t.BaselineCost
		totals.ActualCost += t.ActualCost
		totals.Effect += t.Effect
		totals.EssNet += t.EssNet
		totals.Load += t.Load
		totals.PV += t.PV
		totals.GridImport += t.GridImport
		totals.GridExport += t.GridExport
		totals.EssCharged += t.EssCharged
		totals.EssDischarged += t.EssDischarged
		totals.PVToLoad += t.PVToLoad
		totals.PVToEss += t.PVToEss
		totals.PVToGrid += t.PVToGrid
		totals.GridToLoad += t.GridToLoad
		totals.GridToEss += t.GridToEss
		totals.EssToLoad += t.EssToLoad
		totals.EssToGrid += t.EssToGrid
		totals.RevenuePvExport += t.RevenuePvExport
		totals.RevenuePvSelf += t.RevenuePvSelf
		totals.RevenueEssExport += t.RevenueEssExport
		totals.RevenueEssSelf += t.RevenueEssSelf
		totals.ExpenseGridCharge += t.ExpenseGridCharge
		totals.EssWithdrawnCost += t.EssWithdrawnCost
		totals.EssRealizedProfit += t.EssRealizedProfit
		totals.EssDegradationCost += t.EssDegradationCost
		totals.HoursWithData += t.HoursWithData
		totals.HoursMissingPrice += t.HoursMissingPrice

		// kWh-weighted price reconstruction. avg_*_price is itself a
		// kWh-weighted ratio over the day, so multiplying back by the
		// day's kWh recovers the exact numerator — no precision loss.
		importNum += t.AvgImportPrice * t.GridImport
		importDen += t.GridImport
		exportNum += t.AvgExportPrice * t.GridExport
		exportDen += t.GridExport

		hasData := t.HoursWithData > 0
		if hasData {
			totals.DaysWithData++
			if !bestSet || t.Effect > totals.BestDay.EffectUah {
				totals.BestDay = MonthExtreme{Date: key, EffectUah: t.Effect}
				bestSet = true
			}
			if !minSet || t.Effect < totals.MinEffectDay.EffectUah {
				totals.MinEffectDay = MonthExtreme{Date: key, EffectUah: t.Effect}
				minSet = true
			}
		}

		var rdnAvg float64
		if acc := dayRdn[key]; acc != nil && acc.den > 0 {
			rdnAvg = acc.num / acc.den
		}
		var cycles float64
		if capacityKwh > 0 {
			cycles = t.EssDischarged / capacityKwh
		}

		// Fact-vs-optimum on the PV-free basis: realised effect adds back
		// the export-opportunity of PV that was stored (so charging from
		// PV counts as free, like the optimum and the example).
		do := dayOpts[key]
		factPv0 := t.EssNet
		if do != nil {
			factPv0 += do.pvChargeOpp
		}
		opt := computeOptimum(do, factPv0)
		totals.EssFact += opt.fact
		totals.EssOptimum += opt.optimum
		totals.EssReserveTiming += opt.timing
		totals.EssReserveSoc += opt.soc
		totals.EssReservePv += opt.pv
		totals.EssPvMissedKwh += opt.pvMissed

		outDays = append(outDays, MonthDay{
			Date:             key,
			Totals:           t,
			RdnAvgUahPerKwh:  rdnAvg,
			EquivalentCycles: cycles,
			IsFinal:          d.IsFinal,
			EssFact:          opt.fact,
			EssOptimum:       opt.optimum,
			EssReserve:       opt.reserve,
			EssReserveTiming: opt.timing,
			EssReserveSoc:    opt.soc,
			EssReservePv:     opt.pv,
			EssPvMissedKwh:   opt.pvMissed,
		})
	}

	if importDen > 0 {
		totals.AvgImportPrice = importNum / importDen
	}
	if exportDen > 0 {
		totals.AvgExportPrice = exportNum / exportDen
	}
	if monthRdnDen > 0 {
		totals.RdnAvgUahPerKwh = monthRdnNum / monthRdnDen
	}
	totals.RdnMaxUahPerKwh = monthRdnMax
	totals.RevenueTotal = totals.RevenuePvExport + totals.RevenuePvSelf + totals.RevenueEssExport + totals.RevenueEssSelf
	totals.ExpenseTotal = totals.ExpenseGridCharge
	totals.Ebitda = totals.RevenueTotal - totals.ExpenseTotal
	if capacityKwh > 0 {
		totals.EquivalentCycles = totals.EssDischarged / capacityKwh
	}
	totals.EssReserve = totals.EssOptimum - totals.EssFact
	if totals.EssOptimum > 0 {
		totals.EssCapturedShare = totals.EssFact / totals.EssOptimum
	}

	// ESS EOD snapshot: last day with data wins (point-in-time state).
	for i := len(days) - 1; i >= 0; i-- {
		if days[i].Totals.HoursWithData > 0 {
			totals.EssAvgCostBasisEod = days[i].Totals.EssAvgCostBasisEod
			totals.EssResidualKwhEod = days[i].Totals.EssResidualKwhEod
			totals.EssCostBasisUahEod = days[i].Totals.EssCostBasisUahEod
			break
		}
	}

	// Heatmap rows: one per calendar day present in the daily slice, in
	// order, so the frontend renders a stable grid.
	hm := make([]DayMargin, 0, len(outDays))
	for _, d := range outDays {
		grid := margins[d.Date]
		if grid == nil {
			grid = make([]*float64, 24)
		}
		hm = append(hm, DayMargin{Date: d.Date, Hours: grid})
	}

	return StoredMonth{
		Month:        month,
		Tz:           loc.String(),
		Totals:       totals,
		Days:         outDays,
		HourlyMargin: hm,
		DaysInMonth:  len(outDays),
	}
}

// GetMonth returns economics for one calendar month, read-through: final
// days are served from cache, missing / non-final days are recomputed
// (and persisted) on read. month is YYYY-MM in tz.
func (s *Service) GetMonth(ctx context.Context, orgID, month, tz string) (StoredMonth, error) {
	loc, err := loadLocation(tz)
	if err != nil {
		return StoredMonth{}, err
	}
	parsed, err := time.ParseInLocation("2006-01", month, loc)
	if err != nil {
		return StoredMonth{}, fmt.Errorf("month must be YYYY-MM")
	}
	firstDay := time.Date(parsed.Year(), parsed.Month(), 1, 0, 0, 0, 0, loc)
	nextMonth := firstDay.AddDate(0, 1, 0)
	lastDay := nextMonth.AddDate(0, 0, -1)
	now := time.Now().In(loc)

	// Which stored days are already final (and thus trustworthy)?
	existing, _ := s.backend.LoadDailyRange(ctx, orgID, firstDay, lastDay)
	finalSet := make(map[string]bool, len(existing))
	for _, d := range existing {
		if d.IsFinal {
			finalSet[d.Day.In(loc).Format("2006-01-02")] = true
		}
	}

	// Recompute every day that is missing or not yet final, but never a
	// day in the future. For a past month every day is final → no work;
	// for the current month only the open tail (today) recomputes.
	recomputed := false
	for day := firstDay; !day.After(lastDay); day = day.AddDate(0, 0, 1) {
		if day.After(now) {
			break
		}
		key := day.Format("2006-01-02")
		if finalSet[key] {
			continue
		}
		if _, err := s.ComputeDay(ctx, orgID, key, tz); err != nil {
			if ctx.Err() != nil {
				return StoredMonth{}, ctx.Err()
			}
			// Best-effort: a single broken day shouldn't fail the month.
			continue
		}
		recomputed = true
	}

	// Reuse the pre-recompute snapshot for a fully-final month (no work
	// happened); only re-read when a day was actually recomputed.
	daily := existing
	if recomputed {
		reloaded, err := s.backend.LoadDailyRange(ctx, orgID, firstDay, lastDay)
		if err != nil {
			return StoredMonth{}, fmt.Errorf("load daily range: %w", err)
		}
		daily = reloaded
	}
	hourly, _ := s.backend.LoadHourlyRange(ctx, orgID, firstDay, nextMonth)

	schedule, _ := s.backend.TariffSchedule(ctx, orgID)
	tariffs, ok := schedule.ResolveForDay(firstDay)
	if !ok {
		tariffs = DefaultTariffs
	}

	result := AggregateMonth(month, loc, daily, hourly, tariffs.EssCapacityKwh, tariffs.DegradationUahPerKwh)
	result.OrganizationID = orgID
	return result, nil
}
