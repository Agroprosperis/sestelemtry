package energyflow

import (
	"fmt"
	"math"
)

// uint32InvalidRaw is the unscaled UINT32 sentinel that some Huawei
// counters emit for "no data" (`0xFFFFFFFF`). After applying gain it
// shows up as e.g. `42949672.95` for gain `0.01` — the spec calls this
// out explicitly. We compare in raw integer space rather than scaled
// floats so we don't depend on the gain.
const uint32InvalidRaw = 4294967295

// IsInvalidUint32Scaled reports whether `value` is suspiciously close
// to `0xFFFFFFFF * gain` — the canonical "no data" sentinel for UINT32
// counters. The tolerance is `gain * 10` per the spec, large enough to
// catch values that were rescaled with slight floating-point drift.
func IsInvalidUint32Scaled(value float64, gain float64) bool {
	if gain == 0 {
		return false
	}
	invalid := uint32InvalidRaw * gain
	return math.Abs(value-invalid) < math.Abs(gain)*10
}

// applyEssSign normalizes a raw Active ESS power reading so that
// positive values mean discharge regardless of which sign convention
// the inverter uses. Mirrors `normalized_ess_power_kw` in the spec.
func applyEssSign(raw float64, sign int) float64 {
	if sign == -1 {
		return -raw
	}
	return raw
}

// Calculate runs one delta interval. `prev` and `curr` must both be
// non-nil; `totals` may be nil if the caller does not want running
// totals updated (tests). On a rejected interval `result.Skipped` is
// true and warnings explain why.
//
// The algorithm follows the spec verbatim:
//
//   1. Validate timestamps, dt, device time skew, sentinel UINT32 values.
//   2. Compute deltas of the five accumulator counters; reject negative
//      deltas (counter rollover / reset) with a warning.
//   3. Charge side: grid_to_ess += Δgrid_supply, then
//      pv_to_ess += max(Δess_charged - Δgrid_supply, 0).
//      If Δgrid_supply > Δess_charged we clamp to Δess_charged and
//      emit a warning instead of producing a negative pv_to_ess.
//   4. Discharge side: ess_to_load = min(Δess_discharged,
//      max(Δload - Δpv, 0)); ess_to_grid = remainder.
//   5. Balance check: |pv_to_ess + grid_to_ess - Δess_charged| and
//      |ess_to_load + ess_to_grid - Δess_discharged| against
//      `BalanceToleranceKwh`; warnings only, never errors.
//
// On success the caller folds the delta into RunningTotals and
// persists.
func Calculate(prev, curr *EnergySample, totals *RunningTotals, opts EnergyFlowOptions) IntervalDelta {
	opts = WithDefaults(opts)
	if prev == nil || curr == nil {
		return IntervalDelta{Skipped: true, Warnings: []string{"missing prev or curr sample"}}
	}
	dt := IntervalDelta{}

	dtSeconds := float64(curr.Timestamp - prev.Timestamp)
	dt.DtSeconds = dtSeconds
	if dtSeconds <= 0 {
		dt.Warnings = append(dt.Warnings, "invalid timestamp: dt <= 0")
		dt.Skipped = true
		return dt
	}
	if opts.MaxGapSeconds > 0 && dtSeconds > float64(opts.MaxGapSeconds) {
		dt.Warnings = append(dt.Warnings, fmt.Sprintf("interval skipped: dt=%.0fs > max_gap=%ds", dtSeconds, opts.MaxGapSeconds))
		dt.Skipped = true
		return dt
	}

	if prev.PvDeviceEpochSeconds != nil && prev.EssDeviceEpochSeconds != nil &&
		curr.PvDeviceEpochSeconds != nil && curr.EssDeviceEpochSeconds != nil {
		skew := absInt64(*curr.PvDeviceEpochSeconds - *curr.EssDeviceEpochSeconds)
		if int(skew) > opts.MaxDeviceTimeSkewSeconds {
			dt.Warnings = append(dt.Warnings, fmt.Sprintf("interval skipped: device time skew=%ds > max=%ds", skew, opts.MaxDeviceTimeSkewSeconds))
			dt.Skipped = true
			return dt
		}
	}

	if !haveAllAccumulators(prev) || !haveAllAccumulators(curr) {
		dt.Warnings = append(dt.Warnings, "interval skipped: missing accumulator counter")
		dt.Skipped = true
		return dt
	}

	if invalidAccumulator(prev) || invalidAccumulator(curr) {
		dt.Warnings = append(dt.Warnings, "interval skipped: UINT32 sentinel value in accumulator")
		dt.Skipped = true
		return dt
	}

	deltaPv := *curr.AccumulatedPvYieldKwh - *prev.AccumulatedPvYieldKwh
	deltaLoad := *curr.AccumulatedLoadKwh - *prev.AccumulatedLoadKwh
	deltaGridToEss := *curr.TotalGridSupplyToEssKwh - *prev.TotalGridSupplyToEssKwh
	deltaEssCharged := *curr.TotalEssChargedKwh - *prev.TotalEssChargedKwh
	deltaEssDischarged := *curr.TotalEssDischargedKwh - *prev.TotalEssDischargedKwh

	if deltaPv < 0 || deltaLoad < 0 || deltaGridToEss < 0 || deltaEssCharged < 0 || deltaEssDischarged < 0 {
		dt.Warnings = append(dt.Warnings, "interval skipped: negative delta on accumulator (rollover or reset)")
		dt.Skipped = true
		return dt
	}

	dt.DeltaPvYieldKwh = deltaPv
	dt.DeltaLoadKwh = deltaLoad
	dt.DeltaGridToEssKwh = deltaGridToEss
	dt.DeltaEssChargedKwh = deltaEssCharged
	dt.DeltaEssDischargedKwh = deltaEssDischarged

	gridToEss := deltaGridToEss
	if gridToEss > deltaEssCharged {
		dt.Warnings = append(dt.Warnings, "delta_grid_to_ess > delta_ess_charged: clamped to delta_ess_charged")
		gridToEss = deltaEssCharged
	}
	pvToEss := deltaEssCharged - gridToEss
	if pvToEss < 0 {
		pvToEss = 0
	}
	dt.GridToEssKwh = gridToEss
	dt.PvToEssKwh = pvToEss

	loadDeficitAfterPv := deltaLoad - deltaPv
	if loadDeficitAfterPv < 0 {
		loadDeficitAfterPv = 0
	}
	essToLoad := deltaEssDischarged
	if essToLoad > loadDeficitAfterPv {
		essToLoad = loadDeficitAfterPv
	}
	essToGrid := deltaEssDischarged - essToLoad
	if essToGrid < 0 {
		essToGrid = 0
	}
	dt.EssToLoadKwh = essToLoad
	dt.EssToGridKwh = essToGrid

	chargeBalance := math.Abs((pvToEss + gridToEss) - deltaEssCharged)
	if chargeBalance > opts.BalanceToleranceKwh {
		dt.Warnings = append(dt.Warnings, fmt.Sprintf("charge balance deviation: |pv_to_ess+grid_to_ess - delta_ess_charged|=%.4f > tol=%.4f", chargeBalance, opts.BalanceToleranceKwh))
	}
	dischargeBalance := math.Abs((essToLoad + essToGrid) - deltaEssDischarged)
	if dischargeBalance > opts.BalanceToleranceKwh {
		dt.Warnings = append(dt.Warnings, fmt.Sprintf("discharge balance deviation: |ess_to_load+ess_to_grid - delta_ess_discharged|=%.4f > tol=%.4f", dischargeBalance, opts.BalanceToleranceKwh))
	}

	if curr.PvDeviceEpochSeconds == nil || curr.EssDeviceEpochSeconds == nil {
		dt.Warnings = append(dt.Warnings, "device_epoch_seconds missing: snapshot is less reliable")
	}

	if totals != nil {
		totals.Add(dt)
	}
	return dt
}

func haveAllAccumulators(s *EnergySample) bool {
	return s.AccumulatedPvYieldKwh != nil &&
		s.AccumulatedLoadKwh != nil &&
		s.TotalGridSupplyToEssKwh != nil &&
		s.TotalEssChargedKwh != nil &&
		s.TotalEssDischargedKwh != nil
}

// invalidAccumulator reports whether any of the five accumulators on
// the sample carries the UINT32 sentinel. We use gain=0.01 because
// every accumulator in the spec uses that scale; a different gain on
// some future register would need its own check.
func invalidAccumulator(s *EnergySample) bool {
	const gain = 0.01
	if s.AccumulatedPvYieldKwh != nil && IsInvalidUint32Scaled(*s.AccumulatedPvYieldKwh, gain) {
		return true
	}
	if s.AccumulatedLoadKwh != nil && IsInvalidUint32Scaled(*s.AccumulatedLoadKwh, gain) {
		return true
	}
	if s.TotalGridSupplyToEssKwh != nil && IsInvalidUint32Scaled(*s.TotalGridSupplyToEssKwh, gain) {
		return true
	}
	if s.TotalEssChargedKwh != nil && IsInvalidUint32Scaled(*s.TotalEssChargedKwh, gain) {
		return true
	}
	if s.TotalEssDischargedKwh != nil && IsInvalidUint32Scaled(*s.TotalEssDischargedKwh, gain) {
		return true
	}
	return false
}

func absInt64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// NormalizedEssPowerKw applies `essDischargeSign` to a raw ESS power
// reading. Exposed for the collector / tests so the same convention
// applies everywhere; positive == discharge after normalization.
func NormalizedEssPowerKw(raw float64, sign int) float64 {
	return applyEssSign(raw, sign)
}
