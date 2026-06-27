package economics

import "math"

// cycleReserveThresholdUah is the minimum per-day УЗЕ reserve that promotes a
// day into the "significant cycles" list rendered by the cycle chart (§1.3).
const cycleReserveThresholdUah = 1000

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
