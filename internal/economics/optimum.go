package economics

import "math"

// optimumParams are the ESS physical limits the optimizer respects. They
// are derived empirically from a month of hourly records (see
// deriveOptimumParams) rather than configured, so the "optimum" is the
// best dispatch within the battery's *demonstrated* operating envelope —
// we never assume power, capacity, or SOC range the unit has not shown.
type optimumParams struct {
	capacityKwh          float64
	degradationUahPerKwh float64
	maxChargeKwh         float64 // peak hourly charge == kW at hourly granularity
	maxDischargeKwh      float64 // peak hourly discharge
	socMinKwh            float64
	socMaxKwh            float64
	rte                  float64 // round-trip efficiency in (0,1]
}

const (
	optimumSocLevels    = 101 // SOC grid granularity (≈1% steps)
	defaultRoundTripEff = 0.90
	minRoundTripEff     = 0.50
	maxRoundTripEff     = 0.99
)

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// deriveOptimumParams estimates the battery's operating envelope from the
// month's own hourly history: peak hourly charge/discharge as the power
// limits, the observed residual-kWh range as the usable SOC window, and
// discharged/charged as the round-trip efficiency. Fallbacks keep the
// optimizer well-defined when ESS activity in the month is sparse.
func deriveOptimumParams(hourly []HourlyRecord, capacityKwh, degradationUahPerKwh float64) optimumParams {
	p := optimumParams{
		capacityKwh:          capacityKwh,
		degradationUahPerKwh: degradationUahPerKwh,
		rte:                  defaultRoundTripEff,
	}
	var sumCharged, sumDischarged float64
	var minRes, maxRes float64
	haveResidual := false
	for _, h := range hourly {
		if h.EssCharged > p.maxChargeKwh {
			p.maxChargeKwh = h.EssCharged
		}
		if h.EssDischarged > p.maxDischargeKwh {
			p.maxDischargeKwh = h.EssDischarged
		}
		sumCharged += h.EssCharged
		sumDischarged += h.EssDischarged
		if h.EssRemainingKwhStart != nil {
			r := *h.EssRemainingKwhStart
			if !haveResidual {
				minRes, maxRes = r, r
				haveResidual = true
			} else {
				if r < minRes {
					minRes = r
				}
				if r > maxRes {
					maxRes = r
				}
			}
		}
	}

	// SOC window: observed residual range, clamped to [0, capacity].
	if haveResidual {
		p.socMinKwh = math.Max(0, minRes)
		p.socMaxKwh = math.Min(capacityKwh, maxRes)
	}
	if p.socMaxKwh <= p.socMinKwh {
		// Missing or degenerate residual signal → use the full pack.
		p.socMinKwh = 0
		p.socMaxKwh = capacityKwh
	}

	// Round-trip efficiency from gross throughput, clamped to a sane band.
	if sumCharged > 0 && sumDischarged > 0 {
		p.rte = clampFloat(sumDischarged/sumCharged, minRoundTripEff, maxRoundTripEff)
	}

	// Power fallbacks: if the battery never moved, allow filling the pack
	// in an hour (the SOC window still bounds the schedule).
	if p.maxChargeKwh <= 0 {
		p.maxChargeKwh = capacityKwh
	}
	if p.maxDischargeKwh <= 0 {
		p.maxDischargeKwh = capacityKwh
	}
	return p
}

// optimumHour is one hour of exogenous context for the optimizer. PV
// generation and load are fixed (we can't change weather or consumption);
// only the battery's charge source and discharge timing/destination are
// decision variables.
type optimumHour struct {
	tradable        bool    // prices known → the battery may act this hour
	importPrice     float64 // all-in import price (UAH/kWh)
	exportPrice     float64 // export price (UAH/kWh)
	pvSurplusKwh    float64 // PV not used by load → chargeable for free
	displaceableKwh float64 // grid import to load → dischargeable at import price

	actualPvChargeKwh   float64 // realised pv_to_ess (cap for the no-extra-PV runs)
	actualGridChargeKwh float64 // realised grid_to_ess (cap for the fixed-charge run)
}

// chargeMode selects which charging the optimizer may use. The ladder of
// progressively-relaxed modes lets us attribute the fact↔optimum gap to
// distinct causes (see AggregateMonth).
type chargeMode int

const (
	// modeFull: store any available PV surplus, charge freely from grid.
	modeFull chargeMode = iota
	// modeNoPV: no more PV than was actually stored; grid charge free.
	modeNoPV
	// modeFixedCharge: charge no more than actually charged each hour
	// (PV and grid) — only discharge timing is re-optimized.
	modeFixedCharge
)

// chargeCaps returns the per-hour PV and grid charge ceilings for a mode.
func (h optimumHour) chargeCaps(mode chargeMode) (pvCap, gridCap float64) {
	switch mode {
	case modeFixedCharge:
		return h.actualPvChargeKwh, h.actualGridChargeKwh
	case modeNoPV:
		return h.actualPvChargeKwh, math.Inf(1)
	default: // modeFull
		return h.pvSurplusKwh, math.Inf(1)
	}
}

// pvChargePriceFor returns the project_net opportunity cost of charging
// the battery from PV this hour: the forgone export revenue. Prices below
// 0.1 UAH/kWh snap to free, so storing PV when the market is ~worthless is
// not penalised (§3.1).
func pvChargePriceFor(h optimumHour) float64 {
	if h.exportPrice < 0.1 {
		return 0
	}
	return h.exportPrice
}

// runOptimumDP solves the forward SOC dynamic program over the given hours
// and returns the best achievable effect at every terminal SOC level (plus
// the start level). project_net accounting: charging from PV costs the
// forgone export price (snapped to 0 below 0.1), grid charging costs the
// import price; discharge earns import (to load) / export (to grid) less
// degradation. PV is filled first, then the grid, so each SOC transition
// reduces to a single greedy source/sink split; mode bounds how much PV /
// grid may be used.
func runOptimumDP(hours []optimumHour, startResidualKwh float64, p optimumParams, mode chargeMode) (f []float64, start int, ok bool) {
	span := p.socMaxKwh - p.socMinKwh
	if span <= 0 || len(hours) == 0 {
		return nil, 0, false
	}
	levels := optimumSocLevels
	step := span / float64(levels-1)
	socOf := func(i int) float64 { return p.socMinKwh + float64(i)*step }

	start = int(math.Round((clampFloat(startResidualKwh, p.socMinKwh, p.socMaxKwh) - p.socMinKwh) / step))
	if start < 0 {
		start = 0
	}
	if start >= levels {
		start = levels - 1
	}

	negInf := math.Inf(-1)
	f = make([]float64, levels)
	for i := range f {
		f[i] = negInf
	}
	f[start] = 0

	etaC := math.Sqrt(p.rte)
	etaD := math.Sqrt(p.rte)
	// Per-hour reachable store-delta in grid steps.
	upLevels := int(math.Ceil((p.maxChargeKwh * etaC) / step))
	downLevels := int(math.Ceil((p.maxDischargeKwh * etaD) / step))
	if upLevels < 1 {
		upLevels = 1
	}
	if downLevels < 1 {
		downLevels = 1
	}

	for _, h := range hours {
		nf := make([]float64, levels)
		for i := range nf {
			nf[i] = negInf
		}
		pvPrice := pvChargePriceFor(h)
		for s := 0; s < levels; s++ {
			if math.IsInf(f[s], -1) {
				continue
			}
			// Idle is always feasible.
			if f[s] > nf[s] {
				nf[s] = f[s]
			}
			if !h.tradable {
				continue
			}
			pvCap, gridCap := h.chargeCaps(mode)
			// Charge: climb to a higher SOC level.
			for d := 1; d <= upLevels; d++ {
				ns := s + d
				if ns >= levels {
					break
				}
				chargeAC := (socOf(ns) - socOf(s)) / etaC
				if chargeAC > p.maxChargeKwh+1e-9 {
					break
				}
				cp := math.Min(pvCap, chargeAC) // PV first (project_net cost)
				cg := chargeAC - cp
				if cg > gridCap+1e-9 {
					break // beyond this the grid cap is exceeded
				}
				v := f[s] - cp*pvPrice - cg*h.importPrice
				if v > nf[ns] {
					nf[ns] = v
				}
			}
			// Discharge: drop to a lower SOC level.
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
				}
			}
		}
		f = nf
	}
	return f, start, true
}

// optimizeDay returns the maximum achievable ESS effect for one civil day
// under perfect foresight, on the project_net basis (see runOptimumDP).
// The terminal SOC is unconstrained, so the day's optimum may end anywhere
// in the SOC window.
func optimizeDay(hours []optimumHour, startResidualKwh float64, p optimumParams, mode chargeMode) float64 {
	f, _, ok := runOptimumDP(hours, startResidualKwh, p, mode)
	if !ok {
		return 0
	}
	best := math.Inf(-1)
	for _, v := range f {
		if v > best {
			best = v
		}
	}
	if math.IsInf(best, -1) {
		return 0
	}
	return best
}

// optimizeMonth runs one continuous SOC dynamic program across ALL hours
// of the month in chronological order (the SOC is carried across day
// boundaries) and returns the best terminal effect subject to
// SOC_end ≥ SOC_start (§3.2). The end-≥-start restriction stops the
// optimum from banking energy it never returns, so it is comparable to the
// realised fact on the same project_net basis.
func optimizeMonth(hours []optimumHour, startResidualKwh float64, p optimumParams, mode chargeMode) float64 {
	f, start, ok := runOptimumDP(hours, startResidualKwh, p, mode)
	if !ok {
		return 0
	}
	best := math.Inf(-1)
	for i := start; i < len(f); i++ {
		if f[i] > best {
			best = f[i]
		}
	}
	if math.IsInf(best, -1) {
		return 0
	}
	return best
}
