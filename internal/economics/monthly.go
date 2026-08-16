package economics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
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
	EssToGrid  float64

	EssCharged    float64
	EssDischarged float64
	EssNet        float64

	// EssWithdrawnCostUah is what the energy discharged this hour had
	// cost to store, at the pack's weighted-average cost (see RollHour).
	// nil for rows persisted before the cost-basis walk existed.
	EssWithdrawnCostUah *float64

	// EssRealizedProfitUah is the hour's discharge revenue minus the
	// weighted-average cost of exactly the energy withdrawn and its wear
	// (see RollHour). Unlike EssNet it carries no charging leg, so it
	// divides cleanly by the hour's discharge. nil for rows persisted
	// before the cost-basis walk existed.
	EssRealizedProfitUah *float64

	EssRemainingKwhStart *float64

	// EssPeakIntervalKw is the peak per-interval (sub-hourly, ~5-min)
	// implied ESS charge/discharge power (kW) observed within this hour,
	// derived from raw telemetry. It lets the anomaly filter catch
	// sub-hourly spikes that the hourly sum averages away (§3.4). 0 means
	// no raw sub-hourly signal was available (filter falls back to the
	// hourly-sum check).
	EssPeakIntervalKw float64
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

// MarginHour is one cell of the ESS marginality heatmap. Margin is the
// headline number (UAH per kWh discharged); the rest is the arithmetic
// behind it, carried so the dashboard can show an operator where the
// figure came from instead of asking them to trust it. The four parts
// satisfy Margin x DischargedKwh = RevenueUah − CostUah − WearUah.
type MarginHour struct {
	Margin        float64
	DischargedKwh float64
	RevenueUah    float64
	// CostUah is the weighted-average cost of storing exactly the energy
	// withdrawn this hour — zero for energy that came from the sun.
	CostUah float64
	WearUah float64
}

// DayMargin is one row of the ESS marginality heatmap: 24 hourly cells
// for one civil day. nil entries are hours the pack sat out — no
// meaningful discharge, no cost basis, or anomalous telemetry.
type DayMargin struct {
	Date  string
	Hours []*MarginHour
}

// MonthExtreme records the day with the best / worst project effect.
type MonthExtreme struct {
	Date      string
	EffectUah float64
}

// dayHasCounterIssue reports whether a day's stored quality flags mark
// its hourly flow split as approximate: the import counter froze or
// lagged behind the ESS charge (import_lag), the day's energy balance
// did not close (load_mismatch), a counter stepped and the day had to be
// scaled onto its daily meter (counter_step), or a canonical FusionSolar
// KPI was rejected as implausible (reconcile_rejected). no_scale and
// load_rebalanced are routine bookkeeping notes and do not count.
func dayHasCounterIssue(flags []string) bool {
	for _, f := range flags {
		if strings.HasPrefix(f, "import_lag") ||
			strings.HasPrefix(f, "load_mismatch") ||
			strings.HasPrefix(f, "counter_step") ||
			strings.HasPrefix(f, "reconcile_rejected") {
			return true
		}
	}
	return false
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

	// FlaggedDays counts days whose quality flags mark the hourly flow
	// split as approximate — frozen / lagging FusionSolar counters
	// (import_lag, load_mismatch) or a rejected canonical KPI. Routine
	// bookkeeping flags (no_scale, load_rebalanced) do not count.
	FlaggedDays int

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

	// EssDataQuality reports the УЗЕ anomaly filter outcome: anomalous hours
	// (physically impossible charge/discharge readings) are excluded from
	// the fact/optimum/reserve above so corrupt telemetry can't distort it.
	EssDataQuality DataQuality

	BestDay      MonthExtreme
	MinEffectDay MonthExtreme
}

// DataQuality summarises the ESS (УЗЕ) anomaly filter for a period.
// Anomalous hours have a sub-hourly peak (or hourly charge/discharge) above
// the unit's power limit × tolerance and are dropped from the
// fact/optimum/reserve; the rest of the day stays.
type DataQuality struct {
	DataOK                     bool
	TotalDays                  int
	AnomalousHours             int
	AnomalousDays              int
	AnomalousDates             []string
	// Anomalies lists each excluded hour with classified reasons
	// (peak spike, hourly over-limit, after a telemetry gap).
	Anomalies                  []AnomalyHour
	// ReasonCounts rolls up Anomalies[*].Reasons for portfolio / notes.
	ReasonCounts               map[string]int
	MaxChargeKwhPerInterval    float64
	MaxDischargeKwhPerInterval float64
	PowerLimitKwhPerInterval   float64
	// MaxIntervalPowerKw is the largest sub-hourly (~5-min) implied ESS
	// power (kW) seen in the period, from raw telemetry. It is the signal
	// the per-interval anomaly check compares against the day's power
	// limit · tol.
	MaxIntervalPowerKw float64
}

// Anomaly reason codes returned in DataQuality (stable API values).
const (
	AnomalyReasonPeakSpike       = "peak_spike"        // sub-hourly implied power > limit
	AnomalyReasonHourlyOverLimit = "hourly_over_limit" // hourly charge/discharge > limit
	AnomalyReasonAfterGap        = "after_gap"         // hour follows a multi-hour data hole
)

// AnomalyHour is one excluded УЗЕ hour with the signals that triggered it.
type AnomalyHour struct {
	At             string   // RFC3339 hour start in the aggregation tz
	Date           string   // YYYY-MM-DD civil date
	Hour           int      // 0..23 local
	Reasons        []string // AnomalyReason* codes
	PeakKw         float64
	ChargedKwh     float64
	DischargedKwh  float64
}

// essAnomalyTolerance is how far above the nominal per-interval power limit
// a reading may go before the hour is treated as corrupt telemetry.
const essAnomalyTolerance = 1.5

// detectEssAnomalies flags individual hours with corrupt ESS telemetry —
// readings physically impossible for the unit. Two signals trigger the
// flag: (1) the sub-hourly peak power (EssPeakIntervalKw, from raw ~5-min
// telemetry) exceeding the day's power limit · tol, which catches spikes
// the hourly sum averages away; and (2), as a fallback when no raw signal
// is present, an hourly charge/discharge above that limit · 1h · tol.
// Hours that immediately follow a multi-hour hole in the series are also
// tagged after_gap (typical after a connection break). It returns the set
// of anomalous hour starts (Unix seconds) plus a DataQuality summary.
//
// The ceiling comes from ratingsOn(hour), so a plant that gained a pack
// mid-period is judged by the power it actually had that day; a day whose
// ratings resolve to zero power has the filter disabled (no hour of it is
// excluded).
func detectEssAnomalies(hourly []HourlyRecord, loc *time.Location, ratingsOn func(time.Time) EssRatings, tol float64) (map[int64]bool, DataQuality) {
	badHours := make(map[int64]bool)
	dq := DataQuality{DataOK: true}
	for _, h := range hourly {
		if h.EssCharged > dq.MaxChargeKwhPerInterval {
			dq.MaxChargeKwhPerInterval = h.EssCharged
		}
		if h.EssDischarged > dq.MaxDischargeKwhPerInterval {
			dq.MaxDischargeKwhPerInterval = h.EssDischarged
		}
		if h.EssPeakIntervalKw > dq.MaxIntervalPowerKw {
			dq.MaxIntervalPowerKw = h.EssPeakIntervalKw
		}
	}
	if ratingsOn == nil || len(hourly) == 0 {
		return badHours, dq
	}

	sorted := append([]HourlyRecord(nil), hourly...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].HourStart.Before(sorted[j].HourStart)
	})

	// The reported ceiling is the one in force at the end of the period, so
	// a grown plant reports its current rating — the same convention the
	// year rollup uses when it keeps the latest month's value.
	dq.PowerLimitKwhPerInterval = ratingsOn(sorted[len(sorted)-1].HourStart).PowerLimitKw

	badDays := make(map[string]bool)
	reasonCounts := make(map[string]int)
	anomalies := make([]AnomalyHour, 0)
	for i, h := range sorted {
		limit := ratingsOn(h.HourStart).PowerLimitKw * tol // hourly granularity → 1h interval
		if limit <= 0 {
			continue
		}
		peakOver := h.EssPeakIntervalKw > 0 && h.EssPeakIntervalKw > limit
		hourlyOver := h.EssCharged > limit || h.EssDischarged > limit
		var reasons []string
		if h.EssPeakIntervalKw > 0 {
			if peakOver {
				reasons = append(reasons, AnomalyReasonPeakSpike)
			}
			// Peak path is authoritative for "is this hour corrupt"; still
			// note hourly over-limit when both fire.
			if peakOver && hourlyOver {
				reasons = append(reasons, AnomalyReasonHourlyOverLimit)
			}
			if !peakOver {
				continue
			}
		} else {
			if !hourlyOver {
				continue
			}
			reasons = append(reasons, AnomalyReasonHourlyOverLimit)
		}
		local := h.HourStart.In(loc)
		date := local.Format("2006-01-02")
		// Tag after_gap when earlier the same civil day there was real
		// activity, then a multi-hour idle hole (typical connection loss —
		// economics still stores zero-filled hours, so HourStart gaps alone
		// miss this), then this anomalous hour.
		if hasIdleHoleAfterActivity(sorted, i, loc, date) {
			reasons = append(reasons, AnomalyReasonAfterGap)
		} else {
			// Fallback: multi-hour hole in HourStart sequence same day.
			for j := i - 1; j >= 0; j-- {
				prevLocal := sorted[j].HourStart.In(loc)
				if prevLocal.Format("2006-01-02") != date {
					break
				}
				nextStart := sorted[j+1].HourStart
				if nextStart.Sub(sorted[j].HourStart) > time.Hour+time.Minute {
					reasons = append(reasons, AnomalyReasonAfterGap)
					break
				}
			}
		}
		badHours[h.HourStart.Unix()] = true
		badDays[date] = true
		for _, r := range reasons {
			reasonCounts[r]++
		}
		anomalies = append(anomalies, AnomalyHour{
			At:            local.Format(time.RFC3339),
			Date:          date,
			Hour:          local.Hour(),
			Reasons:       reasons,
			PeakKw:        h.EssPeakIntervalKw,
			ChargedKwh:    h.EssCharged,
			DischargedKwh: h.EssDischarged,
		})
	}
	dq.AnomalousHours = len(badHours)
	dq.AnomalousDays = len(badDays)
	dq.AnomalousDates = make([]string, 0, len(badDays))
	for k := range badDays {
		dq.AnomalousDates = append(dq.AnomalousDates, k)
	}
	sort.Strings(dq.AnomalousDates)
	dq.Anomalies = anomalies
	dq.ReasonCounts = reasonCounts
	dq.DataOK = dq.AnomalousHours == 0
	return badHours, dq
}

// isIdleHour reports a telemetry hour with no energy movement — used to
// spot connection-loss holes that are still stored as zero-filled rows.
func isIdleHour(h HourlyRecord) bool {
	return h.EssCharged == 0 && h.EssDischarged == 0 &&
		h.PVToEss == 0 && h.PVToGrid == 0 && h.PVToLoad == 0 &&
		h.GridImport == 0 && h.GridToEss == 0 && h.GridToLoad == 0 &&
		h.EssToLoad == 0
}

// hasIdleHoleAfterActivity is true when, earlier the same civil day, there
// was at least one non-idle hour followed by ≥2 consecutive idle hours
// before index i (the anomalous hour).
func hasIdleHoleAfterActivity(sorted []HourlyRecord, i int, loc *time.Location, date string) bool {
	hadActive := false
	idleRun := 0
	for j := 0; j < i; j++ {
		if sorted[j].HourStart.In(loc).Format("2006-01-02") != date {
			continue
		}
		if !isIdleHour(sorted[j]) {
			hadActive = true
			idleRun = 0
			continue
		}
		if !hadActive {
			continue
		}
		idleRun++
		if idleRun >= 2 {
			return true
		}
	}
	return false
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

// socRun is one stretch of hours the monthly SOC dynamic program can
// solve as a whole: the days in it share the same УЗЕ ratings, hence the
// same SOC grid and power ceiling.
type socRun struct {
	params   optimumParams
	hours    []optimumHour
	startKwh float64
}

// dailyRatings memoizes the per-day ratings lookup by civil date: the
// resolver walks the tariff schedule while a month asks for the same day
// once per hour. The power fallback is applied here, so every consumer
// sees the same ceiling for a day.
func dailyRatings(loc *time.Location, ratingsFor func(day time.Time) EssRatings) func(time.Time) EssRatings {
	cache := make(map[string]EssRatings, 31)
	return func(t time.Time) EssRatings {
		local := t.In(loc)
		key := local.Format("2006-01-02")
		if r, ok := cache[key]; ok {
			return r
		}
		var r EssRatings
		if ratingsFor != nil {
			day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
			r = ratingsFor(day).withPowerFallback()
		}
		cache[key] = r
		return r
	}
}

// heatmapMinDischargeKwh is the discharge an hour must show before the
// marginality heatmap gives it a cell. A pack that gave back a fraction
// of a kWh was idling, not trading, and its per-kWh ratio says more
// about the divisor than about the hour.
const heatmapMinDischargeKwh = 1.0

// AggregateMonth folds the persisted daily + hourly records of one month
// into a MonthlyTotals + per-day breakdown + ESS marginality heatmap.
//
// ratingsFor gives the УЗЕ ratings in effect on a given day. Every УЗЕ
// number below is judged by the ratings of its own day, because the plant
// can grow mid-month (an extra pack, more inverters): freezing the
// 1st-of-month version would flag the new unit's honest power as corrupt
// telemetry, rate its throughput against the old capacity, and solve the
// optimum inside an envelope the plant has outgrown.
func AggregateMonth(month string, loc *time.Location, days []DailyRecord, hourly []HourlyRecord, ratingsFor func(day time.Time) EssRatings) StoredMonth {
	ratingsOn := dailyRatings(loc, ratingsFor)
	badHours, dq := detectEssAnomalies(hourly, loc, ratingsOn, essAnomalyTolerance)
	dq.TotalDays = len(days)
	isBadHour := func(h HourlyRecord) bool {
		return badHours[h.HourStart.Unix()]
	}
	// Per-day RDN stats keyed by civil date, derived from the hourly
	// slice (the daily table stores only the all-in import price).
	type rdnAcc struct {
		num float64 // sum(rdn * import)
		den float64 // sum(import)
	}
	dayRdn := make(map[string]*rdnAcc)
	// Per-day optimizer contexts (24 hourly slots + the day's opening SOC
	// residual), shared with the single-day plan path so both render the
	// same DP schedule.
	dayOpts := buildDayOpts(hourly, loc, isBadHour)
	margins := make(map[string][]*MarginHour)
	var monthRdnNum, monthRdnDen, monthRdnMax float64
	haveRdnMax := false

	for _, h := range hourly {
		local := h.HourStart.In(loc)
		key := local.Format("2006-01-02")
		hour := local.Hour()
		grid := margins[key]
		if grid == nil {
			grid = make([]*MarginHour, 24)
			margins[key] = grid
		}
		if hour >= 0 && hour < 24 && h.EssDischarged >= heatmapMinDischargeKwh &&
			!isBadHour(h) && h.EssRealizedProfitUah != nil && h.EssWithdrawnCostUah != nil {
			m := *h.EssRealizedProfitUah / h.EssDischarged
			if !math.IsInf(m, 0) && !math.IsNaN(m) {
				revenue := h.EssToLoad*h.ImportPrice + h.EssToGrid*h.ExportPrice
				grid[hour] = &MarginHour{
					Margin:        m,
					DischargedKwh: h.EssDischarged,
					RevenueUah:    revenue,
					CostUah:       *h.EssWithdrawnCostUah,
					// Derived from the identity RollHour used, so the parts
					// always reconstruct the margin shown — recomputing wear
					// from today's tariff would not if the tariff has moved.
					WearUah: revenue - *h.EssWithdrawnCostUah - *h.EssRealizedProfitUah,
				}
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
	if len(badHours) > 0 {
		cleanHourly = make([]HourlyRecord, 0, len(hourly))
		for _, h := range hourly {
			if isBadHour(h) {
				continue
			}
			cleanHourly = append(cleanHourly, h)
		}
	}
	// One envelope per distinct ratings set, derived from the hours that
	// ran under it: after an expansion the old pack is still judged by the
	// power and SOC window it had, and the new one by its own.
	cleanByRatings := make(map[EssRatings][]HourlyRecord)
	for _, h := range cleanHourly {
		r := ratingsOn(h.HourStart)
		cleanByRatings[r] = append(cleanByRatings[r], h)
	}
	paramsCache := make(map[EssRatings]optimumParams, len(cleanByRatings))
	paramsFor := func(r EssRatings) optimumParams {
		if p, ok := paramsCache[r]; ok {
			return p
		}
		p := deriveOptimumParams(cleanByRatings[r], r.CapacityKwh, r.DegradationUahPerKwh, r.PowerLimitKw, r.RoundtripEff)
		paramsCache[r] = p
		return p
	}

	// computeOptimum solves the 3-run ladder for one day: each relaxation
	// only adds freedom, so the values are monotonic and the three
	// reasons are non-negative and sum to the reserve.
	computeOptimum := func(do *dayOpt, factPv0 float64, p optimumParams) optimumResult {
		if do == nil {
			return optimumResult{fact: factPv0, optimum: factPv0}
		}
		start := p.socMinKwh
		if do.haveStart {
			start = do.startKwh
		}
		fixed := optimizeDay(do.hours[:], start, p, modeFixedCharge)
		noPv := optimizeDay(do.hours[:], start, p, modeNoPV)
		full := optimizeDay(do.hours[:], start, p, modeFull)
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
	var uzeCycles []UzeCycle

	// Chronological hour lists for the continuous monthly SOC DP (§3.2),
	// built over non-anomalous days only. The DP is solved on one capacity
	// grid, so a mid-month ratings change starts a new run instead: each
	// run carries SOC across its own day boundaries and the headline sums
	// them. A plant that did not change keeps the single continuous DP.
	var socRuns []*socRun
	var curRun *socRun
	var curRatings EssRatings
	var monthFact float64

	for _, d := range days {
		t := d.Totals
		key := d.Day.In(loc).Format("2006-01-02")
		ratings := ratingsOn(d.Day)
		params := paramsFor(ratings)

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
			if dayHasCounterIssue(t.QualityFlags) {
				totals.FlaggedDays++
			}
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
		if ratings.CapacityKwh > 0 {
			cycles = t.EssDischarged / ratings.CapacityKwh
		}
		totals.EquivalentCycles += cycles

		// Fact-vs-optimum on the project_net basis: the realised fact is
		// Σ EssNet over non-anomalous hours (PV charge is already costed at
		// the forgone export in HourEconomicsFor). Only the corrupt hours
		// are dropped — the rest of the day still feeds reserve / cycles.
		do := dayOpts[key]
		factEssNet := t.EssNet
		if do != nil && dq.AnomalousHours > 0 {
			factEssNet = do.essNet
		}
		opt := computeOptimum(do, factEssNet, params)
		totals.EssPvMissedKwh += opt.pvMissed
		monthFact += factEssNet
		if opt.reserve >= cycleReserveThresholdUah {
			if cyc, ok := buildCycle(key, do, factEssNet, params, ratings.CapacityKwh, ratings.PowerLimitKw, loc); ok {
				uzeCycles = append(uzeCycles, cyc)
			}
		}
		if do != nil {
			if curRun == nil || ratings != curRatings {
				curRun = &socRun{params: params, startKwh: params.socMinKwh}
				if do.haveStart {
					curRun.startKwh = do.startKwh
				}
				socRuns = append(socRuns, curRun)
				curRatings = ratings
			}
			curRun.hours = append(curRun.hours, do.hours[:]...)
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
	// Authoritative monthly headline: a continuous SOC DP across the
	// non-anomalous hours of the month (SOC carried across day boundaries,
	// SOC_end ≥ SOC_start), summed over the ratings runs. The 3-mode ladder
	// attributes the reserve to its causes; clamp to a monotonic ladder
	// bottomed at the realised fact so every component stays ≥ 0. The
	// per-day MonthDay rows above remain a per-day decomposition for the
	// daily-detail table.
	var monthFixed, monthNoPv, monthFull float64
	for _, run := range socRuns {
		monthFixed += optimizeMonth(run.hours, run.startKwh, run.params, modeFixedCharge)
		monthNoPv += optimizeMonth(run.hours, run.startKwh, run.params, modeNoPV)
		monthFull += optimizeMonth(run.hours, run.startKwh, run.params, modeFull)
	}
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
			grid = make([]*MarginHour, 24)
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

	result := AggregateMonth(month, loc, daily, hourly, schedule.EssRatingsFor)
	result.OrganizationID = orgID
	return result, nil
}
