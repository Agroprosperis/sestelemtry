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
//
// roundtripEff lets a per-object config pin the round-trip efficiency: a
// value > 0 overrides the empirical estimate (clamped to the sane band),
// while 0 keeps the demonstrated-throughput estimate (§2.4).
func deriveOptimumParams(hourly []HourlyRecord, capacityKwh, degradationUahPerKwh, powerLimitKw, roundtripEff float64) optimumParams {
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

	// SOC window. With a per-object config (powerLimitKw > 0) the optimum is
	// the perfect-foresight dispatch within the unit's PHYSICAL envelope, so
	// the pack may swing across its full usable capacity [0, capacityKwh] —
	// matching the reference optimize_dp, whose states span 0..capacity_kwh.
	// Without config we fall back to the observed residual range so an
	// unconfigured object can't claim headroom its telemetry never showed.
	if powerLimitKw > 0 {
		p.socMinKwh = 0
		p.socMaxKwh = capacityKwh
	} else if haveResidual {
		p.socMinKwh = math.Max(0, minRes)
		p.socMaxKwh = math.Min(capacityKwh, maxRes)
	}
	if p.socMaxKwh <= p.socMinKwh {
		// Missing or degenerate residual signal → use the full pack.
		p.socMinKwh = 0
		p.socMaxKwh = capacityKwh
	}

	// Round-trip efficiency: a configured value (per-object) wins;
	// otherwise estimate from gross throughput, clamped to a sane band.
	if roundtripEff > 0 {
		p.rte = clampFloat(roundtripEff, minRoundTripEff, maxRoundTripEff)
	} else if sumCharged > 0 && sumDischarged > 0 {
		p.rte = clampFloat(sumDischarged/sumCharged, minRoundTripEff, maxRoundTripEff)
	}

	// Per-hour charge/discharge ceiling. A per-object config (powerLimitKw)
	// pins it to the unit's nameplate power — the buckets are hourly, so the
	// kW rating equals the kWh movable in one hour. This matches the
	// reference optimize_dp (power_per_step = power_kw / 12 over 5-min steps,
	// i.e. power_kw kWh per hour) and lets the optimum arbitrage the full
	// physical throughput rather than only what the battery actually moved.
	if powerLimitKw > 0 {
		p.maxChargeKwh = powerLimitKw
		p.maxDischargeKwh = powerLimitKw
	}
	// Power fallbacks: if the battery never moved (and no config), allow
	// filling the pack in an hour (the SOC window still bounds the schedule).
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
//
// Granularity is HOURLY by deliberate design, not an unfinished port of
// the 5-minute reference (§3.2): the day-ahead market (РДН) clears on an
// hourly grid, so import/export prices are constant within the hour and
// the price arbitrage the reserve measures is fully captured at hourly
// resolution. A finer (e.g. 5-minute) DP would only refine intra-hour SOC
// and power tracking — it cannot change the optimal value when the price
// signal itself is hourly — while multiplying the DP step count ~12× per
// day. We therefore lock the optimizer to hourly buckets; the per-object
// power ceiling that finer steps would enforce is handled separately by
// the УЗЕ anomaly filter (EssPowerLimitKw).
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

// socGrid is the SOC discretisation the DP solvers share: optimumSocLevels
// evenly spaced levels over [socMinKwh, socMaxKwh], the level nearest the
// start residual, and how many levels one hour can climb / drop.
type socGrid struct {
	levels     int
	step       float64
	start      int
	etaC, etaD float64
	upLevels   int
	downLevels int
}

func newSocGrid(startResidualKwh float64, p optimumParams) (socGrid, bool) {
	span := p.socMaxKwh - p.socMinKwh
	if span <= 0 {
		return socGrid{}, false
	}
	g := socGrid{levels: optimumSocLevels}
	g.step = span / float64(g.levels-1)
	g.start = int(math.Round((clampFloat(startResidualKwh, p.socMinKwh, p.socMaxKwh) - p.socMinKwh) / g.step))
	g.start = max(0, min(g.start, g.levels-1))
	g.etaC = math.Sqrt(p.rte)
	g.etaD = math.Sqrt(p.rte)
	// Per-hour reachable store-delta in grid steps; no move spans more
	// than the whole grid.
	g.upLevels = min(max(1, int(math.Ceil((p.maxChargeKwh*g.etaC)/g.step))), g.levels-1)
	g.downLevels = min(max(1, int(math.Ceil((p.maxDischargeKwh*g.etaD)/g.step))), g.levels-1)
	return g, true
}

func (g socGrid) socOf(p optimumParams, level int) float64 {
	return p.socMinKwh + float64(level)*g.step
}

// chargeSplit is the AC energy that climbs d levels, split PV first
// (project_net cost), then grid. ok is false once the move exceeds the
// power ceiling or the grid cap — and so does every larger move.
func (g socGrid) chargeSplit(d int, pvCap, gridCap float64, p optimumParams) (cp, cg float64, ok bool) {
	chargeAC := float64(d) * g.step / g.etaC
	if chargeAC > p.maxChargeKwh+1e-9 {
		return 0, 0, false
	}
	cp = chargeAC
	if pvCap < cp {
		cp = pvCap
	}
	cg = chargeAC - cp
	if cg > gridCap+1e-9 {
		return 0, 0, false
	}
	return cp, cg, true
}

// dischargeSplit is the AC energy that dropping d levels delivers,
// displacing load first, the rest exported. ok is false once the move
// exceeds the power ceiling (and so does every larger move).
func (g socGrid) dischargeSplit(d int, displaceableKwh float64, p optimumParams) (dl, dg, dischargeAC float64, ok bool) {
	dischargeAC = float64(d) * g.step * g.etaD
	if dischargeAC > p.maxDischargeKwh+1e-9 {
		return 0, 0, 0, false
	}
	dl = dischargeAC
	if displaceableKwh < dl {
		dl = displaceableKwh
	}
	return dl, dischargeAC - dl, dischargeAC, true
}

// hourMoves prices one hour's feasible SOC moves. A move's value depends
// only on how many levels it spans, never on the level it starts from, so
// each is priced once per hour and the DP sweep is a plain add-and-compare.
// chargeCost[d-1] is the cost of climbing d levels, dischargeGain[d-1] the
// net revenue of dropping d levels; a non-tradable hour has neither.
type hourMoves struct {
	chargeCost    []float64
	dischargeGain []float64
}

func (m *hourMoves) price(h optimumHour, g socGrid, p optimumParams, mode chargeMode) {
	m.chargeCost = m.chargeCost[:0]
	m.dischargeGain = m.dischargeGain[:0]
	if !h.tradable {
		return
	}
	pvPrice := pvChargePriceFor(h)
	pvCap, gridCap := h.chargeCaps(mode)
	for d := 1; d <= g.upLevels; d++ {
		cp, cg, ok := g.chargeSplit(d, pvCap, gridCap, p)
		if !ok {
			break
		}
		m.chargeCost = append(m.chargeCost, cp*pvPrice+cg*h.importPrice)
	}
	for d := 1; d <= g.downLevels; d++ {
		dl, dg, dischargeAC, ok := g.dischargeSplit(d, h.displaceableKwh, p)
		if !ok {
			break
		}
		m.dischargeGain = append(m.dischargeGain, dl*h.importPrice+dg*h.exportPrice-dischargeAC*p.degradationUahPerKwh)
	}
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
	if len(hours) == 0 {
		return nil, 0, false
	}
	g, ok := newSocGrid(startResidualKwh, p)
	if !ok {
		return nil, 0, false
	}

	negInf := math.Inf(-1)
	f = make([]float64, g.levels)
	nf := make([]float64, g.levels)
	for i := range f {
		f[i] = negInf
	}
	f[g.start] = 0

	var mv hourMoves
	for _, h := range hours {
		mv.price(h, g, p, mode)
		if len(mv.chargeCost) == 0 && len(mv.dischargeGain) == 0 {
			// Idle is the only move, so every level keeps its value.
			continue
		}
		copy(nf, f) // idle is always feasible
		for s, fs := range f {
			if fs == negInf {
				continue
			}
			// Charge: climb to a higher SOC level.
			if n := min(len(mv.chargeCost), g.levels-1-s); n > 0 {
				up := nf[s+1 : s+1+n]
				for i, c := range mv.chargeCost[:n] {
					if v := fs - c; v > up[i] {
						up[i] = v
					}
				}
			}
			// Discharge: drop to a lower SOC level.
			if n := min(len(mv.dischargeGain), s); n > 0 {
				down := nf[s-n : s]
				for i, gain := range mv.dischargeGain[:n] {
					if v := fs + gain; v > down[n-1-i] {
						down[n-1-i] = v
					}
				}
			}
		}
		f, nf = nf, f
	}
	return f, g.start, true
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
