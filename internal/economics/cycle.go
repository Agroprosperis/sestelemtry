package economics

import (
	"fmt"
	"math"
	"time"
)

// cycleReserveThresholdUah is the minimum per-day УЗЕ reserve that promotes a
// day into the "significant cycles" list rendered by the cycle chart (§1.3).
const cycleReserveThresholdUah = 1000

// dayOpt is one civil day's optimizer context: the 24 hourly slots, the
// residual at the earliest hour (the day's starting SOC), and the
// accumulators the fact-vs-optimum decomposition needs.
type dayOpt struct {
	hours     [24]optimumHour
	raw       [24]HourlyRecord // raw hourly slice for the cycle chart's fact series
	hasRaw    [24]bool
	badHour   [24]bool // anomalous hours zeroed for ESS optimum / fact
	startKwh  float64
	startHour int
	haveStart bool
	pvSurplus float64 // Σ available PV surplus
	actualPv  float64 // Σ actual pv_to_ess
	essNet    float64 // Σ EssNet excluding anomalous hours
}

// addHour folds one hourly record into the day's optimizer context. An
// anomalous hour keeps its slot (prices / SOC continuity) but contributes
// no ESS activity, so a single corrupt spike can't wipe the whole day.
func (do *dayOpt) addHour(hour int, h HourlyRecord, bad bool) {
	do.raw[hour] = h
	do.hasRaw[hour] = true
	if bad {
		do.badHour[hour] = true
		oh := optimumHour{}
		if h.Rdn != nil {
			oh.tradable = true
			oh.importPrice = h.ImportPrice
			oh.exportPrice = h.ExportPrice
		}
		do.hours[hour] = oh
	} else {
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
		do.pvSurplus += pvSurplus
		do.actualPv += h.PVToEss
		do.essNet += h.EssNet
	}
	if h.EssRemainingKwhStart != nil && (!do.haveStart || hour < do.startHour) {
		do.startKwh = *h.EssRemainingKwhStart
		do.startHour = hour
		do.haveStart = true
	}
}

// buildDayOpts groups hourly records into per-civil-day optimizer contexts
// keyed by YYYY-MM-DD in loc. isBadHour flags anomalous telemetry; a nil
// predicate treats every hour as clean.
func buildDayOpts(hourly []HourlyRecord, loc *time.Location, isBadHour func(HourlyRecord) bool) map[string]*dayOpt {
	out := make(map[string]*dayOpt)
	for _, h := range hourly {
		local := h.HourStart.In(loc)
		hour := local.Hour()
		if hour < 0 || hour >= 24 {
			continue
		}
		key := local.Format("2006-01-02")
		do := out[key]
		if do == nil {
			do = &dayOpt{}
			out[key] = do
		}
		bad := isBadHour != nil && isBadHour(h)
		do.addHour(hour, h, bad)
	}
	return out
}

// socPctOf maps usable kWh onto the pack's 10–90% operating band: empty
// usable → 10%, full usable → 90%, i.e. capacity/80 kWh per pack-percent
// (6.45 kWh/1% for a 516 kWh usable pack, §3.7).
func socPctOf(kwh, capacityKwh float64) float64 {
	if capacityKwh <= 0 {
		return 0
	}
	return clampFloat(10+kwh/capacityKwh*80, 0, 100)
}

// socKwhOf inverts socPctOf. Exact for any residual inside the usable
// pack (which maps to 10–90%, well clear of the clamp).
func socKwhOf(pct, capacityKwh float64) float64 {
	if capacityKwh <= 0 {
		return 0
	}
	return math.Max(0, (pct-10)/80*capacityKwh)
}

// buildCycle reconstructs one day's optimal dispatch (modeFull) by
// backtracking the SOC DP and packages it with the realised fact series
// into a UzeCycle for the expandable cycle chart (§1.3 / §3.6). Returns
// ok=false when the day's SOC window is degenerate.
//
// The SOC trajectory and the charge/discharge bars must come from this one
// physically-consistent DP schedule — never from a separate "cosmetic"
// pass (see the methodology's граблі №1, where a display-only reshuffle
// drew 308 kWh of discharge next to a 20% SOC).
func buildCycle(key string, do *dayOpt, fact float64, p optimumParams, capacityKwh, powerLimitKw float64, loc *time.Location) (UzeCycle, bool) {
	if do == nil {
		return UzeCycle{}, false
	}
	start := p.socMinKwh
	if do.haveStart {
		start = do.startKwh
	}
	steps, socStartKwh, optEffect, ok := optimizeDaySchedule(do.hours[:], start, p, modeFull)
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
		SocStart:    socPctOf(socStartKwh, capacityKwh),
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
		soc := socPctOf(s.endResidualKwh, capacityKwh)
		opt.SocPct[i] = &soc
		opt.ExportUah[i] = s.toGridKwh * oh.exportPrice
		opt.LoadUah[i] = s.toLoadKwh * oh.importPrice
		opt.GridCostUah[i] = s.chgGridKwh * oh.importPrice
		dischargeAC := s.toLoadKwh + s.toGridKwh
		sum.ExportVal += opt.ExportUah[i]
		sum.LoadVal += opt.LoadUah[i]
		sum.GridCost += opt.GridCostUah[i]
		sum.ChargePvCost += s.chgPvKwh * pvChargePriceFor(oh)
		sum.Degradation += dischargeAC * p.degradationUahPerKwh
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
			v := socPctOf(*do.raw[i].EssRemainingKwhStart, capacityKwh)
			fc.SocStart = &v
			break
		}
	}
	for i := 0; i+1 < n; i++ {
		if do.hasRaw[i+1] && do.raw[i+1].EssRemainingKwhStart != nil {
			v := socPctOf(*do.raw[i+1].EssRemainingKwhStart, capacityKwh)
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

// dispatchStep is one hour of the optimal dispatch recovered by backtracking
// the SOC DP: how much was discharged to load/grid and charged from PV/grid,
// plus the SOC residual (kWh) at the END of the hour.
type dispatchStep struct {
	toLoadKwh      float64
	toGridKwh      float64
	chgPvKwh       float64
	chgGridKwh     float64
	endResidualKwh float64
}

// optimizeDaySchedule runs the project_net SOC DP over one day's hours and
// backtracks the optimal action per hour. It returns the per-hour dispatch,
// the start residual (kWh), the total effect, and ok=false when the SOC
// window is degenerate. mode bounds the charge sources (use modeFull for the
// displayed optimum cycle). Mirrors runOptimumDP's transition costs exactly,
// adding per-(hour, level) parent pointers for path reconstruction.
func optimizeDaySchedule(hours []optimumHour, startResidualKwh float64, p optimumParams, mode chargeMode) (steps []dispatchStep, startKwh, effect float64, ok bool) {
	span := p.socMaxKwh - p.socMinKwh
	if span <= 0 || len(hours) == 0 {
		return nil, startResidualKwh, 0, false
	}
	levels := optimumSocLevels
	step := span / float64(levels-1)
	socOf := func(i int) float64 { return p.socMinKwh + float64(i)*step }

	start := int(math.Round((clampFloat(startResidualKwh, p.socMinKwh, p.socMaxKwh) - p.socMinKwh) / step))
	if start < 0 {
		start = 0
	}
	if start >= levels {
		start = levels - 1
	}

	negInf := math.Inf(-1)
	f := make([]float64, levels)
	for i := range f {
		f[i] = negInf
	}
	f[start] = 0

	etaC := math.Sqrt(p.rte)
	etaD := math.Sqrt(p.rte)
	upLevels := int(math.Ceil((p.maxChargeKwh * etaC) / step))
	downLevels := int(math.Ceil((p.maxDischargeKwh * etaD) / step))
	if upLevels < 1 {
		upLevels = 1
	}
	if downLevels < 1 {
		downLevels = 1
	}

	type action struct {
		prev           int
		cp, cg, dl, dg float64
	}
	H := len(hours)
	parent := make([][]action, H)
	for hi, h := range hours {
		nf := make([]float64, levels)
		par := make([]action, levels)
		for i := range nf {
			nf[i] = negInf
			par[i] = action{prev: -1}
		}
		pvPrice := pvChargePriceFor(h)
		for s := 0; s < levels; s++ {
			if math.IsInf(f[s], -1) {
				continue
			}
			// Idle is always feasible.
			if f[s] > nf[s] {
				nf[s] = f[s]
				par[s] = action{prev: s}
			}
			if !h.tradable {
				continue
			}
			pvCap, gridCap := h.chargeCaps(mode)
			for d := 1; d <= upLevels; d++ {
				ns := s + d
				if ns >= levels {
					break
				}
				chargeAC := (socOf(ns) - socOf(s)) / etaC
				if chargeAC > p.maxChargeKwh+1e-9 {
					break
				}
				cp := math.Min(pvCap, chargeAC)
				cg := chargeAC - cp
				if cg > gridCap+1e-9 {
					break
				}
				v := f[s] - cp*pvPrice - cg*h.importPrice
				if v > nf[ns] {
					nf[ns] = v
					par[ns] = action{prev: s, cp: cp, cg: cg}
				}
			}
			for d := 1; d <= downLevels; d++ {
				ns := s - d
				if ns < 0 {
					break
				}
				dischargeAC := (socOf(s) - socOf(ns)) * etaD
				if dischargeAC > p.maxDischargeKwh+1e-9 {
					break
				}
				dl := math.Min(h.displaceableKwh, dischargeAC)
				dg := dischargeAC - dl
				v := f[s] + dl*h.importPrice + dg*h.exportPrice - dischargeAC*p.degradationUahPerKwh
				if v > nf[ns] {
					nf[ns] = v
					par[ns] = action{prev: s, dl: dl, dg: dg}
				}
			}
		}
		parent[hi] = par
		f = nf
	}

	best := negInf
	bestLevel := start
	for i, v := range f {
		if v > best {
			best = v
			bestLevel = i
		}
	}
	if math.IsInf(best, -1) {
		return nil, socOf(start), 0, false
	}

	steps = make([]dispatchStep, H)
	lvl := bestLevel
	for hi := H - 1; hi >= 0; hi-- {
		a := parent[hi][lvl]
		steps[hi] = dispatchStep{
			toLoadKwh:      a.dl,
			toGridKwh:      a.dg,
			chgPvKwh:       a.cp,
			chgGridKwh:     a.cg,
			endResidualKwh: socOf(lvl),
		}
		if a.prev >= 0 {
			lvl = a.prev
		}
	}
	return steps, socOf(start), best, true
}

// UzeCycle is one significant УЗЕ day (reserve ≥ cycleReserveThresholdUah)
// with the full hourly optimal-vs-fact schedule the cycle chart renders.
type UzeCycle struct {
	StartDate       string
	EndDate         string
	Label           string
	ActualEffectUah float64
	OptEffectUah    float64
	ReserveUah      float64
	CapturePct      float64
	Chart           CycleChart
}

// CycleChart is the per-hour data behind one cycle's stacked-bar chart.
type CycleChart struct {
	Labels      []string
	CapacityKwh float64
	PowerKw     float64
	Optimal     CycleOptimal
	Fact        CycleFact
	Summary     CycleSummary
}

// CycleOptimal is the optimal dispatch per hour (discharge by destination,
// charge by source, SOC trajectory and per-hour revenue/cost).
type CycleOptimal struct {
	ToLoadKwh   []float64
	ToGridKwh   []float64
	ChgPvKwh    []float64
	ChgGridKwh  []float64
	SocPct      []*float64
	SocStart    float64
	ExportUah   []float64
	LoadUah     []float64
	GridCostUah []float64
}

// CycleFact is the realised УЗЕ behaviour per hour (signed power, SOC, RDN).
type CycleFact struct {
	EssKw    []float64
	SocPct   []*float64
	SocStart *float64
	Rdn      []float64
}

// CycleSummary aggregates the optimal-vs-fact totals for a cycle.
type CycleSummary struct {
	Optimal CycleSummaryOptimal
	Fact    CycleSummaryFact
}

// CycleSummaryOptimal is the optimal-cycle waterfall (revenue legs, costs).
type CycleSummaryOptimal struct {
	EffectUah     float64
	ExportVal     float64
	LoadVal       float64
	ChargePvCost  float64
	GridCost      float64
	Degradation   float64
	ChargePvKwh   float64
	ChargeGridKwh float64
	DischargeKwh  float64
}

// CycleSummaryFact is the realised cycle effect (project_net EssNet).
type CycleSummaryFact struct {
	EffectUah float64
}
