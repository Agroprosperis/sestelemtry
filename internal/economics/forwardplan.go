package economics

import (
	"fmt"
	"math"
	"time"
)

// Forward day-planner (EMS edge, level A): a SOC dynamic program over
// the horizon "now → end of tomorrow" that turns DAM prices + a PV
// forecast + a load profile into an hourly virtual dispatch plan for
// the edge manifest (plan.intervals[]).
//
// Unlike the retrospective /api/v1/uze-plan optimum (perfect foresight
// over realised flows), this one runs on FORECASTS and is the plan the
// shadow engine follows in the economic_arbitrage preset. Mechanics
// mirror cycle.go's optimizeDaySchedule (same SOC-level DP, same
// project_net price semantics: PV charge at export opportunity price,
// grid charge at import price, discharge into load valued at import
// price, discharge beyond the local deficit exported at export price,
// degradation per discharged kWh). ExportAllowed=false caps discharge
// by the local deficit — then the plan can only displace imports.

// ForwardHour is one hour of forecast inputs.
type ForwardHour struct {
	TS           time.Time
	RdnUahPerKwh *float64 // nil = no DAM price published for the hour
	PvKw         float64  // PV forecast, AC kW
	LoadKw       float64  // load forecast (heuristic profile), kW
}

// ForwardParams bounds the DP.
type ForwardParams struct {
	Tariffs            Tariffs
	CapacityKwh        float64 // nameplate ESS capacity
	PowerKw            float64 // charge/discharge AC limit
	SocMinPct          float64 // economic band floor (e.g. 20)
	SocMaxPct          float64 // economic band ceiling (e.g. 90)
	StartSocPct        float64 // SOC now (measured; band midpoint if unknown)
	GridTargetImportKw float64 // cap on grid draw while charging; 0 = uncapped
	// ExportAllowed lets the DP discharge past the local deficit and
	// sell the excess at the export price (cycle.go parity). False
	// keeps the legacy behaviour: discharge only displaces imports.
	ExportAllowed bool
}

// ForwardStep is one planned hour.
type ForwardStep struct {
	TS            time.Time
	Tradable      bool    // false when the hour had no DAM price (plan omits it)
	EssKw         float64 // + discharge / − charge (AC net over the hour)
	ChargePvKwh   float64
	ChargeGridKwh float64
	// DischargeKwh is the TOTAL AC discharge; DischargeGridKwh is the
	// part exported past the local deficit (0 unless ExportAllowed).
	DischargeKwh     float64
	DischargeGridKwh float64
	SocEndPct        float64
	RdnUahPerKwh     float64
	Action           string // charge | discharge | hold
}

// ImportExportPrices computes the all-in hourly prices from the RDN
// price — the same composition HourEconomicsFor uses.
func ImportExportPrices(t Tariffs, rdnUahPerKwh float64) (importPrice, exportPrice float64) {
	vat := 1.0
	if t.IncludeVat {
		vat = 1 + t.VatRate
	}
	importPrice = (rdnUahPerKwh +
		t.DistributionUahPerKwh +
		t.TransmissionUahPerKwh +
		t.SupplierMarginFor(rdnUahPerKwh) +
		t.OtherFeesUahPerKwh) * vat
	exportPrice = rdnUahPerKwh * (1 - t.ExportDiscount) * vat
	return importPrice, exportPrice
}

const forwardSocLevels = 81

// BuildForwardPlan runs the forward DP. Hours must be consecutive and
// hourly; the returned steps are 1:1 with the input hours.
func BuildForwardPlan(hours []ForwardHour, p ForwardParams) ([]ForwardStep, error) {
	if len(hours) == 0 {
		return nil, fmt.Errorf("forwardplan: no hours")
	}
	if p.CapacityKwh <= 0 || p.PowerKw <= 0 {
		return nil, fmt.Errorf("forwardplan: capacity and power must be > 0")
	}
	if p.SocMaxPct <= p.SocMinPct {
		return nil, fmt.Errorf("forwardplan: SOC band is degenerate (%g..%g)", p.SocMinPct, p.SocMaxPct)
	}

	minKwh := p.SocMinPct / 100 * p.CapacityKwh
	maxKwh := p.SocMaxPct / 100 * p.CapacityKwh
	span := maxKwh - minKwh
	step := span / float64(forwardSocLevels-1)
	socOf := func(i int) float64 { return minKwh + float64(i)*step }
	pctOf := func(kwh float64) float64 { return kwh / p.CapacityKwh * 100 }

	rte := p.Tariffs.RoundtripEfficiency
	if rte <= 0 || rte > 1 {
		rte = 0.9
	}
	etaC := math.Sqrt(rte)
	etaD := math.Sqrt(rte)
	degradation := p.Tariffs.DegradationUahPerKwh

	startKwh := clampFloat(p.StartSocPct/100*p.CapacityKwh, minKwh, maxKwh)
	start := int(math.Round((startKwh - minKwh) / step))

	// Terminal value: energy left above the floor is worth what it can
	// displace later (mean import price over the horizon), discounted
	// by the discharge efficiency. Prevents both the "dump everything
	// before midnight" and the "never discharge" end-of-horizon biases.
	meanImport := 0.0
	tradable := 0
	for _, h := range hours {
		if h.RdnUahPerKwh != nil {
			imp, _ := ImportExportPrices(p.Tariffs, *h.RdnUahPerKwh)
			meanImport += imp
			tradable++
		}
	}
	if tradable > 0 {
		meanImport /= float64(tradable)
	}
	terminalValue := func(level int) float64 {
		return (socOf(level) - minKwh) * meanImport * etaD * 0.9
	}

	negInf := math.Inf(-1)
	f := make([]float64, forwardSocLevels)
	for i := range f {
		f[i] = negInf
	}
	f[start] = 0

	upLevels := int(math.Ceil((p.PowerKw * etaC) / step))
	downLevels := int(math.Ceil((p.PowerKw * etaD) / step))

	type action struct {
		prev           int
		cp, cg, dl, dg float64
	}
	parents := make([][]action, len(hours))

	for hi, h := range hours {
		nf := make([]float64, forwardSocLevels)
		par := make([]action, forwardSocLevels)
		for i := range nf {
			nf[i] = negInf
			par[i] = action{prev: -1}
		}
		var importPrice, exportPrice float64
		if h.RdnUahPerKwh != nil {
			importPrice, exportPrice = ImportExportPrices(p.Tariffs, *h.RdnUahPerKwh)
		}
		pvSurplus := math.Max(0, h.PvKw-h.LoadKw)    // kWh over 1h
		displaceable := math.Max(0, h.LoadKw-h.PvKw) // kWh over 1h
		gridCap := p.PowerKw                         // grid-charge cap, kWh
		if p.GridTargetImportKw > 0 {
			gridCap = math.Min(gridCap, math.Max(0, p.GridTargetImportKw-displaceable))
		}

		for s := 0; s < forwardSocLevels; s++ {
			if math.IsInf(f[s], -1) {
				continue
			}
			if f[s] > nf[s] {
				nf[s] = f[s]
				par[s] = action{prev: s}
			}
			if h.RdnUahPerKwh == nil {
				continue // no price — planner leaves the hour to preset rules
			}
			for d := 1; d <= upLevels; d++ {
				ns := s + d
				if ns >= forwardSocLevels {
					break
				}
				chargeAC := (socOf(ns) - socOf(s)) / etaC
				if chargeAC > p.PowerKw+1e-9 {
					break
				}
				cp := math.Min(pvSurplus, chargeAC)
				cg := chargeAC - cp
				if cg > gridCap+1e-9 {
					break
				}
				v := f[s] - cp*exportPrice - cg*importPrice
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
				if dischargeAC > p.PowerKw+1e-9 {
					break
				}
				// Discharge covers the local deficit first (import
				// price); the remainder is exported — but only when the
				// preset allows export, otherwise it caps the action.
				dl := math.Min(dischargeAC, displaceable)
				dg := dischargeAC - dl
				if !p.ExportAllowed && dg > 1e-9 {
					break
				}
				v := f[s] + dl*importPrice + dg*exportPrice - dischargeAC*degradation
				if v > nf[ns] {
					nf[ns] = v
					par[ns] = action{prev: s, dl: dl, dg: dg}
				}
			}
		}
		parents[hi] = par
		f = nf
	}

	best := negInf
	bestLevel := start
	for i, v := range f {
		if math.IsInf(v, -1) {
			continue
		}
		if tv := v + terminalValue(i); tv > best {
			best = tv
			bestLevel = i
		}
	}
	if math.IsInf(best, -1) {
		return nil, fmt.Errorf("forwardplan: DP found no feasible path")
	}

	steps := make([]ForwardStep, len(hours))
	lvl := bestLevel
	for hi := len(hours) - 1; hi >= 0; hi-- {
		a := parents[hi][lvl]
		h := hours[hi]
		st := ForwardStep{
			TS:               h.TS,
			Tradable:         h.RdnUahPerKwh != nil,
			ChargePvKwh:      a.cp,
			ChargeGridKwh:    a.cg,
			DischargeKwh:     a.dl + a.dg,
			DischargeGridKwh: a.dg,
			SocEndPct:        pctOf(socOf(lvl)),
			Action:           "hold",
		}
		if h.RdnUahPerKwh != nil {
			st.RdnUahPerKwh = *h.RdnUahPerKwh
		}
		st.EssKw = a.dl + a.dg - (a.cp + a.cg)
		if st.EssKw > 1e-9 {
			st.Action = "discharge"
		} else if st.EssKw < -1e-9 {
			st.Action = "charge"
		}
		steps[hi] = st
		if a.prev >= 0 {
			lvl = a.prev
		}
	}
	return steps, nil
}
