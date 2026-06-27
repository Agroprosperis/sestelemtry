package economics

import (
	"context"
	"fmt"
	"math"
	"sort"
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

	// ESS fact-vs-optimum rollup (project_net basis: charging from PV
	// costs the forgone export). EssFact is the realised EssNet; EssOptimum
	// the continuous monthly SOC-DP maximum (SOC_end ≥ SOC_start);
	// EssReserve = max(0, EssOptimum − EssFact); EssCapturedShare =
	// EssFact / EssOptimum. The reserve is split into three causes
	// (timing / pre-peak-SOC / missed-PV) sourced from the same monthly DP.
	EssFact          float64
	EssOptimum       float64
	EssReserve       float64
	EssCapturedShare float64
	EssReserveTiming float64
	EssReserveSoc    float64
	EssReservePv     float64
	EssPvMissedKwh   float64

	// EssDataQuality reports the УЗЕ anomaly filter outcome: anomalous days
	// (physically impossible charge/discharge readings) are excluded from
	// the fact/optimum/reserve above so corrupt telemetry can't distort it.
	EssDataQuality DataQuality

	BestDay      MonthExtreme
	MinEffectDay MonthExtreme
}

// DataQuality summarises the ESS (УЗЕ) anomaly filter for a period.
// Anomalous days have an hourly charge or discharge above the unit's power
// limit × tolerance and are dropped from the fact/optimum/reserve.
type DataQuality struct {
	DataOK                     bool
	TotalDays                  int
	AnomalousDays              int
	AnomalousDates             []string
	MaxChargeKwhPerInterval    float64
	MaxDischargeKwhPerInterval float64
	PowerLimitKwhPerInterval   float64
}

// essAnomalyTolerance is how far above the nominal per-interval power limit
// a reading may go before the day is treated as corrupt telemetry.
const essAnomalyTolerance = 1.5

// detectEssAnomalies flags every civil day whose hourly ESS charge or
// discharge exceeds powerLimitKw · 1h · tol — readings that are physically
// impossible for the unit and almost always corrupt telemetry. It returns
// the set of anomalous civil dates plus a DataQuality summary. When
// powerLimitKw ≤ 0 the filter is disabled (no day is excluded).
func detectEssAnomalies(hourly []HourlyRecord, loc *time.Location, powerLimitKw, tol float64) (map[string]bool, DataQuality) {
	bad := make(map[string]bool)
	dq := DataQuality{DataOK: true, PowerLimitKwhPerInterval: powerLimitKw}
	for _, h := range hourly {
		if h.EssCharged > dq.MaxChargeKwhPerInterval {
			dq.MaxChargeKwhPerInterval = h.EssCharged
		}
		if h.EssDischarged > dq.MaxDischargeKwhPerInterval {
			dq.MaxDischargeKwhPerInterval = h.EssDischarged
		}
	}
	if powerLimitKw <= 0 {
		return bad, dq
	}
	limit := powerLimitKw * tol // hourly granularity → 1h interval
	for _, h := range hourly {
		if h.EssCharged > limit || h.EssDischarged > limit {
			bad[h.HourStart.In(loc).Format("2006-01-02")] = true
		}
	}
	dq.AnomalousDays = len(bad)
	dq.AnomalousDates = make([]string, 0, len(bad))
	for k := range bad {
		dq.AnomalousDates = append(dq.AnomalousDates, k)
	}
	sort.Strings(dq.AnomalousDates)
	dq.DataOK = dq.AnomalousDays == 0
	return bad, dq
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

	// Cycles is the list of significant УЗЕ days (reserve ≥
	// cycleReserveThresholdUah), sorted by reserve desc, each carrying the
	// full hourly optimal-vs-fact schedule the cycle chart renders (§1.3).
	Cycles []UzeCycle
}

// AggregateMonth folds the persisted daily + hourly records of one month
// into a MonthlyTotals + per-day breakdown + ESS marginality heatmap.
// capacityKwh is the ESS capacity used for the equivalent-cycle metric;
// powerLimitKw is the per-interval power ceiling for the ESS anomaly filter
// (≤ 0 falls back to capacityKwh ≈ 1C).
func AggregateMonth(month string, loc *time.Location, days []DailyRecord, hourly []HourlyRecord, capacityKwh, degradationUahPerKwh, powerLimitKw float64) StoredMonth {
	if powerLimitKw <= 0 {
		powerLimitKw = capacityKwh
	}
	badDays, dq := detectEssAnomalies(hourly, loc, powerLimitKw, essAnomalyTolerance)
	dq.TotalDays = len(days)
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
		hours     [24]optimumHour
		raw       [24]HourlyRecord // raw hourly slice for the cycle chart's fact series
		hasRaw    [24]bool
		startKwh  float64
		startHour int
		haveStart bool
		pvSurplus float64 // Σ available PV surplus
		actualPv  float64 // Σ actual pv_to_ess
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
			}
			do.hours[hour] = oh
			do.raw[hour] = h
			do.hasRaw[hour] = true
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
	// project_net basis (PV charge costed at the forgone export) and
	// attribute the reserve to its causes. The per-day numbers feed the
	// daily-detail table; the authoritative monthly headline comes from a
	// single continuous SOC DP across the whole month (below).
	type optimumResult struct {
		fact     float64 // realised EssNet (project_net basis)
		optimum  float64
		reserve  float64
		timing   float64 // discharge not at peak
		soc      float64 // pre-peak SOC / grid charge timing
		pv       float64 // missed PV charging
		pvMissed float64 // available PV surplus that was not stored (kWh)
	}
	// Derive the optimizer envelope from the non-anomalous hours only, so a
	// single corrupt reading can't inflate the demonstrated power / SOC
	// window the optimum is allowed to use.
	cleanHourly := hourly
	if len(badDays) > 0 {
		cleanHourly = make([]HourlyRecord, 0, len(hourly))
		for _, h := range hourly {
			if badDays[h.HourStart.In(loc).Format("2006-01-02")] {
				continue
			}
			cleanHourly = append(cleanHourly, h)
		}
	}
	optParams := deriveOptimumParams(cleanHourly, capacityKwh, degradationUahPerKwh)

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

	// buildCycle reconstructs one day's optimal dispatch (modeFull) by
	// backtracking the SOC DP and packages it with the realised fact series
	// into a UzeCycle for the expandable cycle chart (§1.3 / §3.6). Returns
	// ok=false when the day's SOC window is degenerate.
	socPctOf := func(kwh float64) float64 {
		if capacityKwh <= 0 {
			return 0
		}
		return clampFloat(kwh/capacityKwh*100, 0, 100)
	}
	buildCycle := func(key string, do *dayOpt, fact float64) (UzeCycle, bool) {
		if do == nil {
			return UzeCycle{}, false
		}
		start := optParams.socMinKwh
		if do.haveStart {
			start = do.startKwh
		}
		steps, socStartKwh, optEffect, ok := optimizeDaySchedule(do.hours[:], start, optParams, modeFull)
		if !ok {
			return UzeCycle{}, false
		}
		const n = 24
		opt := CycleOptimal{
			ToLoadKwh:   make([]float64, n),
			ToGridKwh:   make([]float64, n),
			ChgPvKwh:    make([]float64, n),
			ChgGridKwh:  make([]float64, n),
			SocPct:      make([]*float64, n),
			ExportUah:   make([]float64, n),
			LoadUah:     make([]float64, n),
			GridCostUah: make([]float64, n),
			SocStart:    socPctOf(socStartKwh),
		}
		fc := CycleFact{
			EssKw:  make([]float64, n),
			SocPct: make([]*float64, n),
			Rdn:    make([]float64, n),
		}
		labels := make([]string, n)
		dm := key
		if dt, err := time.ParseInLocation("2006-01-02", key, loc); err == nil {
			dm = dt.Format("02.01")
		}
		var sum CycleSummaryOptimal
		for i := 0; i < n; i++ {
			labels[i] = fmt.Sprintf("%s %02d", dm, i)
			oh := do.hours[i]
			s := steps[i]
			opt.ToLoadKwh[i] = s.toLoadKwh
			opt.ToGridKwh[i] = s.toGridKwh
			opt.ChgPvKwh[i] = s.chgPvKwh
			opt.ChgGridKwh[i] = s.chgGridKwh
			soc := socPctOf(s.endResidualKwh)
			opt.SocPct[i] = &soc
			opt.ExportUah[i] = s.toGridKwh * oh.exportPrice
			opt.LoadUah[i] = s.toLoadKwh * oh.importPrice
			opt.GridCostUah[i] = s.chgGridKwh * oh.importPrice
			dischargeAC := s.toLoadKwh + s.toGridKwh
			sum.ExportVal += opt.ExportUah[i]
			sum.LoadVal += opt.LoadUah[i]
			sum.GridCost += opt.GridCostUah[i]
			sum.ChargePvCost += s.chgPvKwh * pvChargePriceFor(oh)
			sum.Degradation += dischargeAC * optParams.degradationUahPerKwh
			sum.ChargePvKwh += s.chgPvKwh
			sum.ChargeGridKwh += s.chgGridKwh
			sum.DischargeKwh += dischargeAC
			if do.hasRaw[i] {
				r := do.raw[i]
				fc.EssKw[i] = r.EssDischarged - r.EssCharged
				if r.Rdn != nil {
					fc.Rdn[i] = *r.Rdn
				}
			}
		}
		sum.EffectUah = optEffect

		// Fact SOC is drawn at hour boundaries: soc_start before hour 0,
		// then end-of-hour i == start-of-hour i+1.
		for i := 0; i < n; i++ {
			if do.hasRaw[i] && do.raw[i].EssRemainingKwhStart != nil {
				v := socPctOf(*do.raw[i].EssRemainingKwhStart)
				fc.SocStart = &v
				break
			}
		}
		for i := 0; i+1 < n; i++ {
			if do.hasRaw[i+1] && do.raw[i+1].EssRemainingKwhStart != nil {
				v := socPctOf(*do.raw[i+1].EssRemainingKwhStart)
				fc.SocPct[i] = &v
			}
		}

		reserve := math.Max(0, optEffect-fact)
		capture := 0.0
		if optEffect != 0 {
			capture = fact / optEffect * 100
		}
		return UzeCycle{
			StartDate:       key,
			EndDate:         key,
			Label:           dm,
			ActualEffectUah: fact,
			OptEffectUah:    optEffect,
			ReserveUah:      reserve,
			CapturePct:      capture,
			Chart: CycleChart{
				Labels:      labels,
				CapacityKwh: capacityKwh,
				PowerKw:     powerLimitKw,
				Optimal:     opt,
				Fact:        fc,
				Summary:     CycleSummary{Optimal: sum, Fact: CycleSummaryFact{EffectUah: fact}},
			},
		}, true
	}

	var totals MonthlyTotals
	var importNum, importDen, exportNum, exportDen float64
	var bestSet, minSet bool
	outDays := make([]MonthDay, 0, len(days))
	var uzeCycles []UzeCycle

	// Chronological hour list for the continuous monthly SOC DP (§3.2),
	// built over non-anomalous days only. monthStart is the residual at the
	// first usable hour (the month's opening SOC); monthFact sums the
	// realised EssNet on the same project_net basis.
	monthHours := make([]optimumHour, 0, len(days)*24)
	monthStart := optParams.socMinKwh
	haveMonthStart := false
	var monthFact float64

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

		// Fact-vs-optimum on the project_net basis: the realised fact is
		// simply EssNet (PV charge is already costed at the forgone export
		// in HourEconomicsFor). Anomalous days (corrupt УЗЕ telemetry) are
		// excluded from the per-day reserve and the monthly headline.
		do := dayOpts[key]
		anomalous := badDays[key]
		var opt optimumResult
		if anomalous {
			opt = optimumResult{} // excluded: no fact/optimum/reserve
		} else {
			opt = computeOptimum(do, t.EssNet)
			totals.EssPvMissedKwh += opt.pvMissed
			monthFact += t.EssNet
			if opt.reserve >= cycleReserveThresholdUah {
				if cyc, ok := buildCycle(key, do, t.EssNet); ok {
					uzeCycles = append(uzeCycles, cyc)
				}
			}
			if do != nil {
				if !haveMonthStart {
					if do.haveStart {
						monthStart = do.startKwh
					}
					haveMonthStart = true
				}
				monthHours = append(monthHours, do.hours[:]...)
			}
		}

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
	// Authoritative monthly headline: one continuous SOC DP across all
	// non-anomalous hours of the month (SOC carried across day boundaries,
	// SOC_end ≥ SOC_start). The 3-mode ladder attributes the reserve to its
	// causes; clamp to a monotonic ladder bottomed at the realised fact so
	// every component stays ≥ 0. The per-day MonthDay rows above remain a
	// per-day decomposition for the daily-detail table.
	monthFixed := optimizeMonth(monthHours, monthStart, optParams, modeFixedCharge)
	monthNoPv := optimizeMonth(monthHours, monthStart, optParams, modeNoPV)
	monthFull := optimizeMonth(monthHours, monthStart, optParams, modeFull)
	if monthFixed < monthFact {
		monthFixed = monthFact
	}
	if monthNoPv < monthFixed {
		monthNoPv = monthFixed
	}
	if monthFull < monthNoPv {
		monthFull = monthNoPv
	}
	totals.EssFact = monthFact
	totals.EssOptimum = monthFull
	totals.EssReserveTiming = monthFixed - monthFact
	totals.EssReserveSoc = monthNoPv - monthFixed
	totals.EssReservePv = monthFull - monthNoPv
	totals.EssReserve = totals.EssOptimum - totals.EssFact
	if totals.EssOptimum > 0 {
		totals.EssCapturedShare = totals.EssFact / totals.EssOptimum
	}
	totals.EssDataQuality = dq

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

	sort.SliceStable(uzeCycles, func(i, j int) bool {
		return uzeCycles[i].ReserveUah > uzeCycles[j].ReserveUah
	})

	return StoredMonth{
		Month:        month,
		Tz:           loc.String(),
		Totals:       totals,
		Days:         outDays,
		HourlyMargin: hm,
		DaysInMonth:  len(outDays),
		Cycles:       uzeCycles,
	}
}

// GetMonth returns economics for one calendar month as a pure read of
// the persisted daily/hourly records — the economics-recompute daemon is
// responsible for keeping the month (including today) up to date, so the
// read path never recomputes live. month is YYYY-MM in tz.
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

	// Pure read: the month rollup serves whatever the
	// economics-recompute daemon has already persisted. We never
	// recompute days live here — a user-facing month read must not scan
	// the uncompressed "hot" telemetry chunks (a single such day can
	// take 10+ s and blow past the client timeout). The daemon keeps the
	// current month's days, including today, warm in the background.
	daily, err := s.backend.LoadDailyRange(ctx, orgID, firstDay, lastDay)
	if err != nil {
		return StoredMonth{}, fmt.Errorf("load daily range: %w", err)
	}
	hourly, _ := s.backend.LoadHourlyRange(ctx, orgID, firstDay, nextMonth)

	schedule, _ := s.backend.TariffSchedule(ctx, orgID)
	tariffs, ok := schedule.ResolveForDay(firstDay)
	if !ok {
		tariffs = DefaultTariffs
	}

	result := AggregateMonth(month, loc, daily, hourly, tariffs.EssCapacityKwh, tariffs.DegradationUahPerKwh, tariffs.EssPowerLimitKw)
	result.OrganizationID = orgID
	return result, nil
}
