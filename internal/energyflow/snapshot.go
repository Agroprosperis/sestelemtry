package energyflow

import "time"

// roleSnapshot is the latest reading from one logical role within a
// recompute bucket. The map key is the source metric_key
// (active_pv_power_kw, accumulated_pv_energy_yield_kwh, …); values are
// the decoded engineering-unit numbers stored in telemetry_samples.
type roleSnapshot struct {
	timestamp time.Time
	values    map[string]float64
}

// mergeAccumulators copies the six cumulative-counter source keys
// from a role-tagged value map into a Sample. Missing keys leave the
// destination unchanged so PV-only and ESS-only roles can be merged
// into one Sample via two successive calls.
func mergeAccumulators(dst *Sample, src map[string]float64) {
	if v, ok := src[SrcAccumulatedPVYieldKwh]; ok {
		dst.AccumulatedPVYieldKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcAccumulatedPurchasedKwh]; ok {
		dst.AccumulatedPurchasedKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcAccumulatedSoldKwh]; ok {
		dst.AccumulatedSoldKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcAccumulatedPowerConsumptionKwh]; ok {
		dst.AccumulatedPowerConsumptionKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcTotalEssChargedKwh]; ok {
		dst.TotalESSChargedKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcTotalEssDischargedKwh]; ok {
		dst.TotalESSDischargedKwh = floatPtrCopy(v)
	}
}

// mergePowers copies the five instantaneous-power source keys from a
// role-tagged value map into a Sample. Same merge semantics as
// mergeAccumulators: missing keys are left untouched.
func mergePowers(dst *Sample, src map[string]float64) {
	if v, ok := src[SrcActivePVPowerKw]; ok {
		dst.PVPowerKw = floatPtrCopy(v)
	}
	if v, ok := src[SrcActiveESSPowerKw]; ok {
		dst.ESSPowerKw = floatPtrCopy(v)
	}
	if v, ok := src[SrcLoadPowerKw]; ok {
		dst.LoadPowerKw = floatPtrCopy(v)
	}
	if v, ok := src[SrcGridConnectedActivePowerKw]; ok {
		dst.GridPowerKw = floatPtrCopy(v)
	}
	if v, ok := src[SrcSocPercent]; ok {
		dst.SOCPercent = floatPtrCopy(v)
	}
}

func floatPtrCopy(v float64) *float64 {
	out := v
	return &out
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
