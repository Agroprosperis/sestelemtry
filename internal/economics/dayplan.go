package economics

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

// planIdleKwh is the per-hour movement below which the optimizer's
// choice is treated as "hold". The DP works on a ~1% SOC grid, so a
// transition can leave a fraction of a kWh of rounding noise that is
// not a real dispatch decision.
const planIdleKwh = 0.5

// Recommendation reason codes (uze_optimization_algorithm_appendix.md
// §7.3). They are machine-readable so the UI can group / filter, while
// ReasonText carries the operator-facing Ukrainian explanation required
// by uze_optimization_module_spec.md §15.
const (
	ReasonDischargePeakImport = "DISCHARGE_PEAK_IMPORT"
	ReasonDischargeToGrid     = "DISCHARGE_TO_GRID"
	ReasonChargePvSurplus     = "CHARGE_PV_SURPLUS"
	ReasonChargeGridCheap     = "CHARGE_GRID_CHEAP"
	ReasonHoldForFuturePeak   = "HOLD_FOR_FUTURE_PEAK"
	ReasonHoldLowPrice        = "HOLD_LOW_PRICE"
	ReasonNoPrice             = "NO_PRICE"
)

// Plan warning codes surfaced next to the recommendation so the operator
// can tell a weak plan from a confident one (spec §16).
const (
	WarnNoPrices         = "NO_RDN"
	WarnPartialPrices    = "PARTIAL_RDN"
	WarnNoSoc            = "NO_SOC"
	WarnTelemetryAnomaly = "TELEMETRY_ANOMALY"
)

// DayPlanHour is one hour of the recommended УЗЕ dispatch.
type DayPlanHour struct {
	Hour int
	// RecommendedEssKw is the average ESS power over the hour, signed
	// like the telemetry metric active_ess_power_kw: positive =
	// discharge, negative = charge. Buckets are hourly, so kWh moved in
	// the hour equals the average kW.
	RecommendedEssKw float64
	// SocPct is the modelled state of charge at the END of the hour,
	// on the pack's 10–90% operating band.
	SocPct float64

	EssToLoadKwh float64
	EssToGridKwh float64
	PvToEssKwh   float64
	GridToEssKwh float64

	// EffectUah is this hour's contribution to the day's optimum on the
	// project_net basis: discharge value less grid-charge cost, less the
	// export forgone by storing PV, less degradation.
	EffectUah float64

	Action     string // "charge" | "discharge" | "hold"
	ReasonCode string
	ReasonText string

	// RecommendedLoadKw is the elevator consumption the same daily energy
	// would be scheduled at to soak up exported PV and cheap РДН hours
	// (see recommendLoad). nil for hours without telemetry.
	RecommendedLoadKw *float64

	Rdn *float64
}

// DayPlanTotals is the day's optimum-vs-fact headline plus the waterfall
// legs behind the optimum.
type DayPlanTotals struct {
	OptimumUah float64
	FactUah    float64
	ReserveUah float64
	// CapturedShare is FactUah / OptimumUah (0..1, may be negative when
	// the realised dispatch lost money). 0 when there is no optimum.
	CapturedShare float64

	ChargePvKwh   float64
	ChargeGridKwh float64
	DischargeKwh  float64

	ExportValUah    float64
	LoadValUah      float64
	ChargePvCostUah float64
	GridCostUah     float64
	DegradationUah  float64
}

// DayPlan is the recommended УЗЕ schedule for one civil day: what an
// optimally-run battery would have done given the day's actual PV, load
// and РДН prices.
//
// This is a retrospective benchmark, not a forecast. Without a load plan
// there is nothing to optimise against for future hours, so the plan is
// solved on realised flows — perfect foresight over the same inputs the
// installation actually saw (uze_fact_vs_optimum_methodology.md §1).
type DayPlan struct {
	OrganizationID string
	Date           string
	Tz             string

	// Available is false when the day has no usable hours or the SOC
	// window is degenerate; the UI then draws no recommendation.
	Available bool

	SocStartPct float64
	CapacityKwh float64
	PowerKw     float64

	Hours    []DayPlanHour
	Totals   DayPlanTotals
	Warnings []string
}

// classifyHour turns one DP step into an operator-facing action plus a
// reason. maxFutureImport is the highest all-in import price over the
// hours still ahead, which is what separates "holding for a better hour"
// from "nothing worth doing".
func classifyHour(s dispatchStep, oh optimumHour, socKwh, maxFutureImport float64) (action, code, text string) {
	discharge := s.toLoadKwh + s.toGridKwh
	charge := s.chgPvKwh + s.chgGridKwh

	switch {
	case discharge > planIdleKwh:
		if s.toLoadKwh >= s.toGridKwh {
			return "discharge", ReasonDischargePeakImport,
				fmt.Sprintf("Розряд %.0f кВт·год на споживання — уникаємо імпорту по %.2f грн/кВт·год",
					discharge, oh.importPrice)
		}
		return "discharge", ReasonDischargeToGrid,
			fmt.Sprintf("Розряд %.0f кВт·год в мережу — ціна експорту %.2f грн/кВт·год вигідна",
				discharge, oh.exportPrice)
	case charge > planIdleKwh:
		if s.chgGridKwh > s.chgPvKwh {
			return "charge", ReasonChargeGridCheap,
				fmt.Sprintf("Заряд %.0f кВт·год з мережі — дешева година, %.2f грн/кВт·год",
					charge, oh.importPrice)
		}
		return "charge", ReasonChargePvSurplus,
			fmt.Sprintf("Заряд %.0f кВт·год від надлишку СЕС", charge)
	case !oh.tradable:
		return "hold", ReasonNoPrice, "Немає ціни РДН на цю годину — рішення не приймається"
	case socKwh > planIdleKwh && maxFutureImport > oh.importPrice:
		return "hold", ReasonHoldForFuturePeak,
			fmt.Sprintf("Утримуємо заряд — попереду дорожча година (до %.2f проти %.2f грн/кВт·год)",
				maxFutureImport, oh.importPrice)
	default:
		return "hold", ReasonHoldLowPrice,
			fmt.Sprintf("Утримуємо — поточна ціна %.2f грн/кВт·год не покриває ККД і знос", oh.importPrice)
	}
}

// loadOf is one hour's consumption in kWh (hourly buckets, so also the
// average kW). For anomalous hours the ESS leg is dropped — same rule as
// dayOpt.addHour — so a corrupt discharge reading can't inflate the
// day's demonstrated base / peak consumption.
func (do *dayOpt) loadOf(i int) float64 {
	r := do.raw[i]
	load := r.PVToLoad + r.GridToLoad
	if !do.badHour[i] {
		load += r.EssToLoad
	}
	return load
}

// recommendLoad redistributes the day's ACTUAL consumption into the hours
// where it would have been cheapest to run — the hourly counterpart of the
// monthly "резерв графіка" (shifting flexible elevator work into exported
// PV, valued at the import−export gap).
//
// Rules, in order of what they protect:
//   - Energy conserved: the elevator still has to process the same volume,
//     so Σ recommended == Σ actual, always.
//   - Base stays put: the day's minimum hourly load (ventilation, office)
//     runs in every hour — only the load ABOVE it is treated as movable.
//   - Demonstrated ceiling: no hour is scheduled above the day's maximum
//     observed hourly load — the elevator can't dry and clean faster than
//     it demonstrated that same day. Deliberately the day's own maximum
//     (not a longer window): the plan then answers "how should THIS day's
//     work have been laid out", never inventing a duty level the day
//     didn't actually run at.
//   - Cheapest hours first: flexible energy fills PV-surplus bands (cost =
//     the forgone export) before grid bands (cost = the all-in import
//     price). Hours without a РДН price take no extra load; whatever can't
//     fit into priced hours waterfills back into them.
//   - No prices at all → the recommendation is the fact: there is nothing
//     to optimise against, and inventing a schedule would be noise.
//
// Deliberately independent of the УЗЕ plan (both are computed from the
// same realised flows): coupling them would need a joint optimisation and
// a load model we don't have yet.
func recommendLoad(do *dayOpt) [24]*float64 {
	var out [24]*float64

	var hours []int
	base, peak, total := math.Inf(1), 0.0, 0.0
	anyTradable := false
	for i := 0; i < 24; i++ {
		if !do.hasRaw[i] {
			continue
		}
		hours = append(hours, i)
		l := do.loadOf(i)
		total += l
		if l < base {
			base = l
		}
		if l > peak {
			peak = l
		}
		if do.hours[i].tradable {
			anyTradable = true
		}
	}
	if len(hours) == 0 || total <= 0 {
		return out
	}
	if !anyTradable {
		for _, i := range hours {
			v := do.loadOf(i)
			out[i] = &v
		}
		return out
	}

	headroom := peak - base
	flexible := total - base*float64(len(hours))
	alloc := make(map[int]float64, len(hours))

	// One price band per source per hour: load served by the hour's own PV
	// costs the export it forgoes, anything beyond that comes from the
	// grid at the all-in import price. PV available to load is everything
	// the panels sent to load or grid — including what the ACTUAL load
	// already self-consumed, otherwise a fully self-consumed solar hour
	// would look import-priced and the grain handling would be "moved" to any
	// hour marginally cheaper than import, losing the free sun. The base
	// load eats its share of that PV first; PV already feeding the
	// battery is left to the УЗЕ plan.
	type band struct {
		hour int
		kwh  float64
		cost float64
	}
	var bands []band
	for _, i := range hours {
		if !do.hours[i].tradable || headroom <= 0 {
			continue
		}
		pvAvail := do.raw[i].PVToLoad + do.raw[i].PVToGrid - base
		pv := math.Min(math.Max(pvAvail, 0), headroom)
		if pv > 0 {
			bands = append(bands, band{hour: i, kwh: pv, cost: do.hours[i].exportPrice})
		}
		if grid := headroom - pv; grid > 0 {
			bands = append(bands, band{hour: i, kwh: grid, cost: do.hours[i].importPrice})
		}
	}
	sort.SliceStable(bands, func(a, b int) bool {
		if bands[a].cost != bands[b].cost {
			return bands[a].cost < bands[b].cost
		}
		return bands[a].hour < bands[b].hour
	})
	left := flexible
	for _, b := range bands {
		if left <= 0 {
			break
		}
		take := math.Min(b.kwh, left)
		// A band shares the hour's headroom with the other band of the
		// same hour, so re-check what's actually still free there.
		if free := headroom - alloc[b.hour]; take > free {
			take = free
		}
		if take <= 0 {
			continue
		}
		alloc[b.hour] += take
		left -= take
	}
	// Whatever priced hours couldn't absorb (some hours unpriced, tight
	// ceiling) waterfills back into the unpriced hours' headroom — the
	// energy has to go somewhere, and "somewhere" should still respect
	// the demonstrated ceiling.
	if left > 1e-9 {
		var open []int
		for _, i := range hours {
			if !do.hours[i].tradable && headroom-alloc[i] > 0 {
				open = append(open, i)
			}
		}
		sort.Slice(open, func(a, b int) bool {
			return headroom-alloc[open[a]] < headroom-alloc[open[b]]
		})
		for n, i := range open {
			share := math.Min(left/float64(len(open)-n), headroom-alloc[i])
			alloc[i] += share
			left -= share
		}
	}

	for _, i := range hours {
		v := base + alloc[i]
		out[i] = &v
	}
	return out
}

// buildDayPlan solves one day's optimal dispatch and packages it for the
// dashboard. do carries the day's hourly optimizer context; p the battery
// envelope. The schedule comes from buildCycle so the daily chart and the
// monthly cycle accordion can never diverge.
func buildDayPlan(key string, do *dayOpt, p optimumParams, capacityKwh, powerLimitKw float64, loc *time.Location) DayPlan {
	plan := DayPlan{
		Date:        key,
		CapacityKwh: capacityKwh,
		PowerKw:     powerLimitKw,
	}
	if do == nil {
		return plan
	}
	cyc, ok := buildCycle(key, do, do.essNet, p, capacityKwh, powerLimitKw, loc)
	if !ok {
		return plan
	}
	opt := cyc.Chart.Optimal

	// Highest import price still ahead at each hour — the yardstick for
	// "hold for a future peak" (the DP already made the decision; this
	// only explains it).
	const n = 24
	maxFutureImport := make([]float64, n)
	next := math.Inf(-1)
	for i := n - 1; i >= 0; i-- {
		maxFutureImport[i] = next
		if do.hours[i].tradable && do.hours[i].importPrice > next {
			next = do.hours[i].importPrice
		}
	}

	plan.Available = true
	plan.SocStartPct = opt.SocStart
	plan.Hours = make([]DayPlanHour, n)
	recLoad := recommendLoad(do)

	var anomalous, priced, missingPrice, withSoc int
	socKwh := 0.0
	if do.haveStart {
		socKwh = do.startKwh
	}
	for i := 0; i < n; i++ {
		oh := do.hours[i]
		discharge := opt.ToLoadKwh[i] + opt.ToGridKwh[i]
		charge := opt.ChgPvKwh[i] + opt.ChgGridKwh[i]
		effect := opt.LoadUah[i] + opt.ExportUah[i] - opt.GridCostUah[i] -
			opt.ChgPvKwh[i]*pvChargePriceFor(oh) - discharge*p.degradationUahPerKwh

		action, code, text := classifyHour(dispatchStep{
			toLoadKwh:  opt.ToLoadKwh[i],
			toGridKwh:  opt.ToGridKwh[i],
			chgPvKwh:   opt.ChgPvKwh[i],
			chgGridKwh: opt.ChgGridKwh[i],
		}, oh, socKwh, maxFutureImport[i])

		soc := 0.0
		if opt.SocPct[i] != nil {
			soc = *opt.SocPct[i]
		}
		row := DayPlanHour{
			Hour:              i,
			RecommendedEssKw:  discharge - charge,
			SocPct:            soc,
			EssToLoadKwh:      opt.ToLoadKwh[i],
			EssToGridKwh:      opt.ToGridKwh[i],
			PvToEssKwh:        opt.ChgPvKwh[i],
			GridToEssKwh:      opt.ChgGridKwh[i],
			EffectUah:         effect,
			Action:            action,
			ReasonCode:        code,
			ReasonText:        text,
			RecommendedLoadKw: recLoad[i],
		}
		if do.hasRaw[i] && do.raw[i].Rdn != nil {
			v := *do.raw[i].Rdn
			row.Rdn = &v
		}
		plan.Hours[i] = row

		// Carry the DP's own trajectory forward: this hour's closing SOC
		// is what the next hour's "hold" explanation reasons about.
		socKwh = socKwhOf(soc, capacityKwh)
		if do.hasRaw[i] {
			if do.raw[i].Rdn == nil {
				missingPrice++
			} else {
				priced++
			}
			if do.raw[i].EssRemainingKwhStart != nil {
				withSoc++
			}
			if do.badHour[i] {
				anomalous++
			}
		}
	}

	sum := cyc.Chart.Summary.Optimal
	plan.Totals = DayPlanTotals{
		OptimumUah:      cyc.OptEffectUah,
		FactUah:         cyc.ActualEffectUah,
		ReserveUah:      cyc.ReserveUah,
		CapturedShare:   cyc.CapturePct / 100,
		ChargePvKwh:     sum.ChargePvKwh,
		ChargeGridKwh:   sum.ChargeGridKwh,
		DischargeKwh:    sum.DischargeKwh,
		ExportValUah:    sum.ExportVal,
		LoadValUah:      sum.LoadVal,
		ChargePvCostUah: sum.ChargePvCost,
		GridCostUah:     sum.GridCost,
		DegradationUah:  sum.Degradation,
	}

	// No price anywhere in the day means the optimizer had nothing to
	// trade against, which is a different (and much louder) problem than
	// a few missing hours.
	switch {
	case priced == 0:
		plan.Warnings = append(plan.Warnings, WarnNoPrices)
	case missingPrice > 0:
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s:%d", WarnPartialPrices, missingPrice))
	}
	if withSoc == 0 {
		plan.Warnings = append(plan.Warnings, WarnNoSoc)
	}
	if anomalous > 0 {
		plan.Warnings = append(plan.Warnings, fmt.Sprintf("%s:%d", WarnTelemetryAnomaly, anomalous))
	}
	return plan
}

// GetDayPlan returns the recommended УЗЕ dispatch for one civil day. It
// is a pure read of the persisted hourly economics (never a live
// recompute), so it costs one indexed range scan plus the DP.
//
// date is YYYY-MM-DD in tz.
func (s *Service) GetDayPlan(ctx context.Context, orgID, date, tz string) (DayPlan, error) {
	loc, err := loadLocation(tz)
	if err != nil {
		return DayPlan{}, err
	}
	parsed, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return DayPlan{}, fmt.Errorf("date must be YYYY-MM-DD")
	}
	dayStart := time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 0, 0, 0, 0, loc)
	dayEnd := dayStart.AddDate(0, 0, 1)

	hourly, err := s.backend.LoadHourlyRange(ctx, orgID, dayStart, dayEnd)
	if err != nil {
		return DayPlan{}, fmt.Errorf("load hourly range: %w", err)
	}

	schedule, _ := s.backend.TariffSchedule(ctx, orgID)
	tariffs, ok := schedule.ResolveForDay(dayStart)
	if !ok {
		tariffs = DefaultTariffs
	}
	capacityKwh := tariffs.EssCapacityKwh
	// Same defaulting as AggregateMonth: without a nameplate power the
	// pack is assumed able to fill itself in an hour, which also keeps
	// deriveOptimumParams on its configured branch (SOC window = the full
	// usable pack) instead of the empirical one that would need a month of
	// history to be meaningful.
	powerLimitKw := tariffs.EssPowerLimitKw
	if powerLimitKw <= 0 {
		powerLimitKw = capacityKwh
	}

	badHours, _ := detectEssAnomalies(hourly, loc, powerLimitKw, essAnomalyTolerance)
	isBadHour := func(h HourlyRecord) bool { return badHours[h.HourStart.Unix()] }

	// The envelope must come from clean hours only: one corrupt reading
	// would otherwise inflate the power / SOC window the optimum is
	// allowed to use.
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
	params := deriveOptimumParams(cleanHourly, capacityKwh, tariffs.DegradationUahPerKwh, powerLimitKw, tariffs.RoundtripEfficiency)

	dayOpts := buildDayOpts(hourly, loc, isBadHour)
	plan := buildDayPlan(date, dayOpts[date], params, capacityKwh, powerLimitKw, loc)
	plan.OrganizationID = orgID
	plan.Tz = loc.String()
	return plan, nil
}
