package energyflow

import (
	"fmt"
	"math"
)

// maxGridStepKw is the grid power no real interval reaches. A larger
// implied import or export is a SmartLogger meter coming back from
// zero before the stitcher has mended it — the same ceiling the
// counter stitcher uses.
const maxGridStepKw = 5000

// GenResult is the generator-aware allocation of one interval (ems-spec
// generator handoff §4.3). It is computed beside the plain Result for
// every interval the supply timeline covers and never changes it.
type GenResult struct {
	Mode   SupplyMode
	GenKwh float64

	PVToESSKwh   float64
	GenToESSKwh  float64
	GridToESSKwh float64
	ESSToLoadKwh float64
	ESSToGridKwh float64
	GenToLoadKwh float64
	PVToLoadKwh  float64
	// LoadKwh is the site's own consumption over the interval.
	LoadKwh float64

	EssChargedKwh    float64
	EssDischargedKwh float64

	// Parts that fell in windows without the grid (generator and
	// island modes); blackout windows feed no load.
	LoadOffgridKwh      float64
	PVToLoadOffgridKwh  float64
	ESSToLoadOffgridKwh float64
	PVToESSOffgridKwh   float64
	PVToLoadIslandKwh   float64
	ESSToLoadIslandKwh  float64

	// OffgridImportKwh / OffgridExportKwh are what the SmartLogger grid
	// meter claimed in an off-grid window: diagnostics, zeroed in the
	// allocation (§4.3). A healthy site reads ~0 here.
	OffgridImportKwh float64
	OffgridExportKwh float64

	Skipped  bool
	Warnings []string
}

// AllocateGen allocates the interval (prev, curr] with the generator
// energy and supply mode of g. Unlike Allocate it keeps an off-grid
// interval whose grid meter dropped to zero: without the grid that
// meter has nothing to say, so import and export are zero (§4.3).
//
//	appliances  = pv + import + ess_dis + G − export − ess_chg   (≥ 0)
//	pv_to_ess   = min(ess_chg, max(pv − appliances, 0))
//	gen_to_ess  = min(ess_chg − pv_to_ess, G)
//	grid_to_ess = ess_chg − pv_to_ess − gen_to_ess
//	gen_to_load = G − gen_to_ess
//
// ess_to_load / ess_to_grid follow Allocate's rule on that appliances.
func AllocateGen(prev, curr Sample, g GenWindow, opts Options) GenResult {
	opts = opts.WithDefaults()
	res := GenResult{Mode: g.Mode, GenKwh: math.Max(g.GenKwh, 0)}
	skip := func(format string, args ...any) GenResult {
		res.Skipped = true
		res.Warnings = append(res.Warnings, fmt.Sprintf(format, args...))
		return res
	}

	dt := curr.Timestamp.Sub(prev.Timestamp).Seconds()
	if bad, reason := validateInterval(dt, opts); bad {
		return skip("interval rejected: %s", reason)
	}
	pv, ok, why := counterDelta(prev.AccumulatedPVYieldKwh, curr.AccumulatedPVYieldKwh, "pv_yield")
	if !ok {
		return skip("%s", why)
	}
	chg, ok, why := counterDelta(prev.TotalESSChargedKwh, curr.TotalESSChargedKwh, "ess_charged")
	if !ok {
		return skip("%s", why)
	}
	dis, ok, why := counterDelta(prev.TotalESSDischargedKwh, curr.TotalESSDischargedKwh, "ess_discharged")
	if !ok {
		return skip("%s", why)
	}
	imp, impOK, impWhy := counterDelta(prev.AccumulatedPurchasedKwh, curr.AccumulatedPurchasedKwh, "grid_import")
	exp, expOK, expWhy := counterDelta(prev.AccumulatedSoldKwh, curr.AccumulatedSoldKwh, "grid_export")
	if g.Mode.Offgrid() {
		if impOK {
			res.OffgridImportKwh = imp
		}
		if expOK {
			res.OffgridExportKwh = exp
		}
		imp, exp = 0, 0
	} else {
		if !impOK {
			return skip("%s", impWhy)
		}
		if !expOK {
			return skip("%s", expWhy)
		}
		if maxKwh := maxGridStepKw * dt / 3600; imp > maxKwh || exp > maxKwh {
			return skip("grid meter step over %.0fs (import %g, export %g kWh)", dt, imp, exp)
		}
	}
	if opts.MaxEssPowerKw > 0 {
		maxKwh := opts.MaxEssPowerKw * dt / 3600
		if chg > maxKwh || dis > maxKwh {
			return skip("ess counter step over %.0fs (charged %g, discharged %g kWh)", dt, chg, dis)
		}
	}

	G := res.GenKwh
	appliances := math.Max(pv+imp+dis+G-exp-chg, 0)
	pvToEss := math.Min(chg, math.Max(pv-appliances, 0))
	genToEss := math.Min(chg-pvToEss, G)
	gridToEss := chg - pvToEss - genToEss
	essToLoad := math.Min(dis, math.Max(appliances-pv, 0))
	essToGrid := math.Max(dis-essToLoad, 0)
	if essToGrid > exp {
		essToGrid = exp
		essToLoad = math.Max(dis-essToGrid, 0)
	}
	pvToGrid := math.Max(exp-essToGrid, 0)

	res.PVToESSKwh = pvToEss
	res.GenToESSKwh = genToEss
	res.GridToESSKwh = gridToEss
	res.ESSToLoadKwh = essToLoad
	res.ESSToGridKwh = essToGrid
	res.GenToLoadKwh = math.Max(G-genToEss, 0)
	res.PVToLoadKwh = math.Max(pv-pvToEss-pvToGrid, 0)
	res.LoadKwh = appliances
	res.EssChargedKwh = chg
	res.EssDischargedKwh = dis
	if g.Mode == SupplyGenerator || g.Mode == SupplyIsland {
		res.LoadOffgridKwh = appliances
		res.PVToLoadOffgridKwh = res.PVToLoadKwh
		res.ESSToLoadOffgridKwh = essToLoad
		res.PVToESSOffgridKwh = pvToEss
	}
	if g.Mode == SupplyIsland {
		res.PVToLoadIslandKwh = res.PVToLoadKwh
		res.ESSToLoadIslandKwh = essToLoad
	}
	return res
}

// counterDelta is curr − prev of one accumulator, with Allocate's
// checks: both readings present and finite, and no step backwards.
func counterDelta(prev, curr *float64, name string) (float64, bool, string) {
	if prev == nil || curr == nil {
		return 0, false, "missing required accumulator " + name
	}
	if !isFiniteFloat(*prev) || !isFiniteFloat(*curr) {
		return 0, false, "non-finite accumulator " + name
	}
	d := *curr - *prev
	if d < 0 {
		return 0, false, fmt.Sprintf("negative delta_%s_kwh=%g (rollover/reset)", name, d)
	}
	return d, true, ""
}
