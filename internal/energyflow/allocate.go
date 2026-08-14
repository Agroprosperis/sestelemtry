package energyflow

import (
	"fmt"
	"math"
)

// Allocate runs the spec algorithm on a pair of consecutive snapshots
// and returns the per-interval kWh deltas of the four directional
// flows. The caller is responsible for accumulating the returned
// per-interval deltas into running cumulative totals — Allocate is
// stateless.
//
// The function never panics. Any validation failure (negative counter
// delta, missing accumulator, dt out of range, NaN/Inf) is reported
// via Result.Skipped and Result.Warnings; the four flow values are
// zero in that case so the caller can blindly sum them without a
// special-case branch.
func Allocate(prev, curr Sample, opts Options) Result {
	opts = opts.WithDefaults()
	var res Result

	dt := curr.Timestamp.Sub(prev.Timestamp).Seconds()
	if skip, reason := validateInterval(dt, opts); skip {
		res.Skipped = true
		res.Warnings = append(res.Warnings, "interval rejected: "+reason)
		return res
	}

	type accField struct {
		name string
		prev *float64
		curr *float64
	}
	fields := []accField{
		{"accumulated_pv_yield_kwh", prev.AccumulatedPVYieldKwh, curr.AccumulatedPVYieldKwh},
		{"accumulated_purchased_kwh", prev.AccumulatedPurchasedKwh, curr.AccumulatedPurchasedKwh},
		{"accumulated_sold_kwh", prev.AccumulatedSoldKwh, curr.AccumulatedSoldKwh},
		{"total_ess_charged_kwh", prev.TotalESSChargedKwh, curr.TotalESSChargedKwh},
		{"total_ess_discharged_kwh", prev.TotalESSDischargedKwh, curr.TotalESSDischargedKwh},
	}
	for _, f := range fields {
		if f.prev == nil || f.curr == nil {
			res.Skipped = true
			res.Warnings = append(res.Warnings, fmt.Sprintf("missing required accumulator %s", f.name))
			return res
		}
		if !isFiniteFloat(*f.prev) || !isFiniteFloat(*f.curr) {
			res.Skipped = true
			res.Warnings = append(res.Warnings, fmt.Sprintf("non-finite accumulator %s", f.name))
			return res
		}
	}

	deltaPVYield := *curr.AccumulatedPVYieldKwh - *prev.AccumulatedPVYieldKwh
	deltaGridImport := *curr.AccumulatedPurchasedKwh - *prev.AccumulatedPurchasedKwh
	deltaGridExport := *curr.AccumulatedSoldKwh - *prev.AccumulatedSoldKwh
	deltaEssCharged := *curr.TotalESSChargedKwh - *prev.TotalESSChargedKwh
	deltaEssDischarged := *curr.TotalESSDischargedKwh - *prev.TotalESSDischargedKwh

	// Stable order matters here: warning text is part of the test
	// surface and downstream alerting may match on it.
	deltas := []struct {
		name string
		v    float64
	}{
		{"delta_pv_yield_kwh", deltaPVYield},
		{"delta_grid_import_kwh", deltaGridImport},
		{"delta_grid_export_kwh", deltaGridExport},
		{"delta_ess_charged_kwh", deltaEssCharged},
		{"delta_ess_discharged_kwh", deltaEssDischarged},
	}
	for _, d := range deltas {
		if d.v < 0 {
			res.Skipped = true
			res.Warnings = append(res.Warnings, fmt.Sprintf("negative %s=%g (rollover/reset)", d.name, d.v))
			return res
		}
	}

	// ESS counter-step guard. An upward jump in total_ess_charged /
	// total_ess_discharged (device counter re-base, firmware resync,
	// corrupted high reading) is indistinguishable from real energy in
	// a pure delta calc, and with the gap guard disabled it dumps the
	// whole jump into a single interval. When a physical ESS power
	// ceiling is configured, reject any interval whose implied average
	// ESS power exceeds it; advancing prev past the discontinuity lets
	// the next interval resume from the new counter base.
	if opts.MaxEssPowerKw > 0 && dt > 0 {
		maxKwh := opts.MaxEssPowerKw * (dt / 3600.0)
		if deltaEssCharged > maxKwh {
			res.Skipped = true
			res.Warnings = append(res.Warnings, fmt.Sprintf("delta_ess_charged_kwh=%g exceeds %g kWh max over %.0fs (counter step)", deltaEssCharged, maxKwh, dt))
			return res
		}
		if deltaEssDischarged > maxKwh {
			res.Skipped = true
			res.Warnings = append(res.Warnings, fmt.Sprintf("delta_ess_discharged_kwh=%g exceeds %g kWh max over %.0fs (counter step)", deltaEssDischarged, maxKwh, dt))
			return res
		}
	}

	// Algebraic appliance consumption (spec §Основні формули):
	//   delta_appliances = delta_pv + delta_grid_import + delta_ess_dis
	//                      - delta_grid_export - delta_ess_charged
	// Negative results are clamped to zero — they signal a brief
	// counter glitch on one of the inputs and would otherwise feed
	// nonsense into the allocation rule.
	deltaAppliances := deltaPVYield + deltaGridImport + deltaEssDischarged - deltaGridExport - deltaEssCharged
	if deltaAppliances < 0 {
		deltaAppliances = 0
	}

	// §Заряд УЗЕ
	//   pv_surplus     = max(delta_pv - delta_appliances, 0)
	//   pv_to_ess      = min(delta_ess_charged, pv_surplus)
	//   grid_to_ess    = delta_ess_charged - pv_to_ess
	//
	// pv_to_ess is capped by the PV the interval actually produced;
	// everything else stays on the grid even when it exceeds
	// delta_grid_import. The purchased-energy accumulator routinely lags
	// the ESS charge counter by minutes to hours (FusionSolar refreshes
	// it sporadically), so an earlier version of this rule — which
	// reassigned the uncovered remainder to PV — booked whole nights of
	// grid charging as solar. The charge physically came through the
	// grid connection (PV cannot exceed its own yield and export is
	// metered separately); the late import deltas land in later
	// intervals and the daily reconciliation against the canonical
	// import KPI absorbs the residual skew.
	pvSurplus := deltaPVYield - deltaAppliances
	if pvSurplus < 0 {
		pvSurplus = 0
	}
	pvToEss := math.Min(deltaEssCharged, pvSurplus)
	gridToEss := deltaEssCharged - pvToEss
	if gridToEss > deltaGridImport {
		res.Warnings = append(res.Warnings, fmt.Sprintf("grid_to_ess=%.3f kWh exceeds delta_grid_import=%.3f kWh (lagging import counter)", gridToEss, deltaGridImport))
	}

	// §Розряд УЗЕ
	//   load_deficit_after_pv = max(delta_appliances - delta_pv, 0)
	//   ess_to_load = min(delta_ess_dis, load_deficit_after_pv)
	//   ess_to_grid = max(delta_ess_dis - ess_to_load, 0)
	//   if ess_to_grid > delta_grid_export:
	//     ess_to_grid = delta_grid_export
	//     ess_to_load = max(delta_ess_dis - ess_to_grid, 0)
	loadDeficitAfterPV := deltaAppliances - deltaPVYield
	if loadDeficitAfterPV < 0 {
		loadDeficitAfterPV = 0
	}
	essToLoad := math.Min(deltaEssDischarged, loadDeficitAfterPV)
	essToGrid := math.Max(deltaEssDischarged-essToLoad, 0)
	if essToGrid > deltaGridExport {
		essToGrid = deltaGridExport
		essToLoad = math.Max(deltaEssDischarged-essToGrid, 0)
	}

	res.PVToESSKwh = pvToEss
	res.GridToESSKwh = gridToEss
	res.ESSToLoadKwh = essToLoad
	res.ESSToGridKwh = essToGrid
	res.EssChargedKwh = deltaEssCharged
	res.EssDischargedKwh = deltaEssDischarged

	// §Перевірка балансу — only emits a warning, never rejects
	// the interval. The two checks are independent so an off-balance
	// charge does not mask an off-balance discharge.
	if opts.BalanceToleranceKwh > 0 {
		if d := math.Abs((pvToEss + gridToEss) - deltaEssCharged); d > opts.BalanceToleranceKwh {
			res.Warnings = append(res.Warnings, fmt.Sprintf("charge balance off by %.4f kWh", d))
		}
		if d := math.Abs((essToLoad + essToGrid) - deltaEssDischarged); d > opts.BalanceToleranceKwh {
			res.Warnings = append(res.Warnings, fmt.Sprintf("discharge balance off by %.4f kWh", d))
		}
	}

	// Diagnostic only: ESS sign-of-power sanity. essDischargeSign
	// is allowed to override the convention; if both prev and curr
	// have an ESSPowerKw and after sign normalization the value is
	// non-zero but inconsistent with the charged/discharged delta,
	// emit a warning. This never rejects the interval.
	if opts.EssDischargeSign == 1 || opts.EssDischargeSign == -1 {
		if curr.ESSPowerKw != nil && isFiniteFloat(*curr.ESSPowerKw) {
			normalized := *curr.ESSPowerKw * float64(opts.EssDischargeSign)
			if normalized > 0 && deltaEssCharged > 0 && deltaEssDischarged == 0 {
				res.Warnings = append(res.Warnings, "ess sign mismatch: ess_power>0 but only delta_ess_charged is non-zero")
			}
			if normalized < 0 && deltaEssDischarged > 0 && deltaEssCharged == 0 {
				res.Warnings = append(res.Warnings, "ess sign mismatch: ess_power<0 but only delta_ess_discharged is non-zero")
			}
		}
	}

	return res
}

// FloatPtr is a small helper that returns a pointer to a float64.
// Callers building Sample literals find it more readable than
// f := 1.0; s := Sample{... &f ...}.
func FloatPtr(v float64) *float64 { return &v }
