package economics

import (
	"fmt"
	"math"
)

// reconcileTolerance is the relative gap above which a residual
// discrepancy between a reconciled daily total and its canonical KPI is
// surfaced as a quality flag. The five measured counters are scaled to
// match exactly; this only governs the derived-load comparison against
// use_power, which is not force-scaled (see reconcileFlows).
const reconcileTolerance = 0.02

// maxReconcileFactor / minReconcileFactor bound the scaling applied to a
// counter when matching its canonical KPI. A healthy FusionSolar daily
// value and the allocator agree to within a small correction, so a factor
// far outside this range means one of the two sides is corrupted — and
// which one depends on the direction.
//
// Above maxReconcileFactor the canonical KPI is the suspect: a single bad
// getKpiStationDay value of 24958 kWh against a computed 67 kWh (factor
// ~369) would scale the day's flows into a multi-MWh artifact, so the
// metric keeps its computed value and raises reconcile_rejected.
//
// Below minReconcileFactor it is the other way round: the hourly counter
// deltas claim many times more energy than the plant's own daily meter
// recorded, which no plant can do. That is the signature of a counter
// step — the seam between two data sources (FusionSolar archive backfill
// and live Modbus) whose lifetime registers start from different origins,
// booked as one hour of energy. There the canonical KPI is the sane side:
// the metric is scaled onto it and raises counter_step.
const (
	maxReconcileFactor = 10.0
	minReconcileFactor = 0.1
)

// CanonicalDaily holds the authoritative FusionSolar daily KPIs
// (getKpiStationDay) used to reconcile a day's computed flows. Load maps
// to use_power; the rest map 1:1 to the measured counters.
type CanonicalDaily struct {
	PV            float64
	Load          float64
	GridImport    float64
	GridExport    float64
	EssCharged    float64
	EssDischarged float64
}

// ReconcileField records the per-quantity reconciliation: the computed
// daily sum (pre-scale), the canonical target, and the applied factor.
type ReconcileField struct {
	Computed  float64 `json:"computed"`
	Canonical float64 `json:"canonical"`
	Factor    float64 `json:"factor"`
}

// ReconcileResult is the outcome of scaling one day's flows to the
// canonical KPIs. Applied is false when no canonical data was available
// (the flows are then untouched).
type ReconcileResult struct {
	Applied bool
	Flags   []string
	Detail  map[string]ReconcileField
}

// reconcileFlows scales the per-hour base and directional flows in place
// so each measured daily total matches its canonical KPI, and returns
// the diagnostics. Scaling the directional pairs by the same factor as
// their parent counter preserves the identities pvToEss+gridToEss =
// essCharged and essToLoad+essToGrid = essDischarged, so DeriveDerivedFlows
// stays consistent. A counter whose computed sum is non-positive is left
// unscaled (factor 1) with a no_scale flag. Derived load is compared
// against use_power but never force-scaled, to keep each hour's energy
// balance intact.
func reconcileFlows(flows []*HourFlows, canonical *CanonicalDaily) ReconcileResult {
	if canonical == nil {
		return ReconcileResult{Applied: false}
	}
	var sumPV, sumImp, sumExp, sumChg, sumDis float64
	for _, f := range flows {
		if f == nil {
			continue
		}
		sumPV += f.PV
		sumImp += f.GridImport
		sumExp += f.GridExport
		sumChg += f.EssCharged
		sumDis += f.EssDischarged
	}

	res := ReconcileResult{Applied: true, Detail: map[string]ReconcileField{}}
	factor := func(name string, computed, target float64) float64 {
		fac := 1.0
		switch {
		case computed <= 0:
			res.Flags = append(res.Flags, "no_scale:"+name)
		default:
			fac = target / computed
			switch {
			case fac > maxReconcileFactor:
				// Canonical KPI implausibly above the computed total —
				// treat it as corrupted and keep the computed flows
				// rather than scaling the day into garbage.
				res.Flags = append(res.Flags, fmt.Sprintf("reconcile_rejected:%s:%.2f", name, fac))
				fac = 1.0
			case fac < minReconcileFactor && target > 0:
				// Computed total implausibly above the canonical KPI —
				// a counter step. Scale onto the daily meter, which is
				// the only sane figure left for the day.
				res.Flags = append(res.Flags, fmt.Sprintf("counter_step:%s:%.4f", name, fac))
			case fac < minReconcileFactor:
				// Canonical zero against a large computed total is
				// ambiguous: the KPI may be genuinely zero or simply
				// absent from the day's FusionSolar record, and scaling
				// to zero would erase real energy on the second reading.
				// Keep the computed value and flag it for review.
				res.Flags = append(res.Flags, fmt.Sprintf("reconcile_rejected:%s:%.2f", name, fac))
				fac = 1.0
			}
		}
		res.Detail[name] = ReconcileField{Computed: computed, Canonical: target, Factor: fac}
		return fac
	}
	fPV := factor("pv", sumPV, canonical.PV)
	fImp := factor("grid_import", sumImp, canonical.GridImport)
	fExp := factor("grid_export", sumExp, canonical.GridExport)
	fChg := factor("ess_charged", sumChg, canonical.EssCharged)
	fDis := factor("ess_discharged", sumDis, canonical.EssDischarged)

	for _, f := range flows {
		if f == nil {
			continue
		}
		f.PV *= fPV
		f.GridImport *= fImp
		f.GridExport *= fExp
		f.EssCharged *= fChg
		f.EssDischarged *= fDis
		f.PVToEss *= fChg
		f.GridToEss *= fChg
		f.EssToLoad *= fDis
		f.EssToGrid *= fDis
		// fChg and fPV are independent, so scaling alone can leave the
		// hour with more "solar" charge than its scaled PV. Shift the
		// excess to the grid side: pvToEss+gridToEss = essCharged stays
		// intact and the PVToEss ≤ PV invariant the allocator guarantees
		// pre-scale survives reconciliation.
		if f.PVToEss > f.PV {
			f.GridToEss += f.PVToEss - f.PV
			f.PVToEss = f.PV
		}
	}

	// Derived load (energy balance of the scaled counters) vs use_power.
	var derivedLoad float64
	for _, f := range flows {
		if f == nil {
			continue
		}
		load, _, _, _ := DeriveDerivedFlows(*f)
		derivedLoad += load
	}
	res.Detail["load"] = ReconcileField{Computed: derivedLoad, Canonical: canonical.Load, Factor: 1}
	if canonical.Load > 0 {
		rel := math.Abs(derivedLoad-canonical.Load) / canonical.Load
		if rel > reconcileTolerance {
			res.Flags = append(res.Flags, fmt.Sprintf("load_mismatch:%.4f", rel))
		}
	}
	return res
}
