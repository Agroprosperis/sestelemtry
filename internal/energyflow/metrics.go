package energyflow

// Synthetic metric_keys produced by the aggregator and written to
// telemetry_samples as cumulative kWh counters. They have no Modbus
// register backing them — the values are derived in-process from the
// SmartLogger accumulators. The four keys are intentionally short
// and stable: the dashboard, the Sankey diagram and the
// /api/v1/energy-summary endpoint all reference them by name.
const (
	MetricPVToESSKwh   = "pv_to_ess_kwh"
	MetricGridToESSKwh = "grid_to_ess_kwh"
	MetricESSToLoadKwh = "ess_to_load_kwh"
	MetricESSToGridKwh = "ess_to_grid_kwh"
)

// SyntheticMetricKeys is the canonical order of the four flow metrics.
// Used by reseed (which queries the latest cumulative value for each)
// and by the API summary list.
var SyntheticMetricKeys = []string{
	MetricPVToESSKwh,
	MetricGridToESSKwh,
	MetricESSToLoadKwh,
	MetricESSToGridKwh,
}

// Source metric_keys this package reads from the existing register
// catalog. Mirrors registers/huawei_smartlogger.yaml. Splitting them
// into PV vs ESS sets lets the collector auto-detect each device's
// Role from the metric_keys whitelist: a device polling any of
// PVRequiredMetrics is the PV SmartLogger, a device polling any of
// ESSRequiredMetrics is the ESS SmartLogger, a device polling both
// sets is a single-SmartLogger site.
const (
	SrcAccumulatedPVYieldKwh          = "accumulated_pv_energy_yield_kwh"
	SrcAccumulatedPurchasedKwh        = "accumulated_electricity_purchased_kwh"
	SrcAccumulatedSoldKwh             = "accumulated_electricity_sold_kwh"
	SrcAccumulatedPowerConsumptionKwh = "accumulated_power_consumption_kwh"
	SrcTotalEssChargedKwh             = "total_energy_charged_kwh"
	SrcTotalEssDischargedKwh          = "total_energy_discharged_kwh"

	SrcActivePVPowerKw            = "active_pv_power_kw"
	SrcActiveESSPowerKw           = "active_ess_power_kw"
	SrcLoadPowerKw                = "load_power_kw"
	SrcGridConnectedActivePowerKw = "grid_connected_active_power_kw"
	SrcSocPercent                 = "soc_percent"
)

// PVRequiredMetrics is the set of cumulative counters that must be
// polled from the PV SmartLogger (or the single SmartLogger) for the
// allocation rule to run. Missing any of these on every device of an
// org disables the energy-flow feature for that org with a warning.
var PVRequiredMetrics = []string{
	SrcAccumulatedPVYieldKwh,
	SrcAccumulatedPurchasedKwh,
	SrcAccumulatedSoldKwh,
}

// ESSRequiredMetrics is the set of cumulative counters that must be
// polled from the ESS SmartLogger (or the single SmartLogger).
var ESSRequiredMetrics = []string{
	SrcTotalEssChargedKwh,
	SrcTotalEssDischargedKwh,
}

// hasAny reports whether any of needles is present in haystack.
func hasAny(haystack map[string]struct{}, needles []string) bool {
	for _, n := range needles {
		if _, ok := haystack[n]; ok {
			return true
		}
	}
	return false
}

// hasAll reports whether every needle is present in haystack.
func hasAll(haystack map[string]struct{}, needles []string) bool {
	for _, n := range needles {
		if _, ok := haystack[n]; !ok {
			return false
		}
	}
	return true
}

// DetectRole classifies a device by the metric_keys it polls.
// A device that owns every PV accumulator and every ESS accumulator
// is a single SmartLogger; one that owns only PV accumulators is the
// PV side of a dual deployment, ditto for ESS. A device that owns
// neither full set returns RoleNone — its samples are still written
// to telemetry_samples by the regular collector path, they just
// don't feed the energy-flow allocator.
//
// The whitelist is a set: order does not matter and unknown keys are
// ignored. Empty input (no whitelist) is treated as "device polls
// the entire catalog" → RoleSingle.
func DetectRole(metricKeys []string) Role {
	if len(metricKeys) == 0 {
		return RoleSingle
	}
	set := make(map[string]struct{}, len(metricKeys))
	for _, k := range metricKeys {
		set[k] = struct{}{}
	}
	hasPV := hasAll(set, PVRequiredMetrics)
	hasESS := hasAll(set, ESSRequiredMetrics)
	switch {
	case hasPV && hasESS:
		return RoleSingle
	case hasPV:
		return RolePV
	case hasESS:
		return RoleESS
	}
	// Partial coverage (e.g. only one of the three PV accumulators)
	// is intentionally treated as RoleNone: a half-populated source
	// would silently feed garbage deltas into the allocator.
	_ = hasAny
	return RoleNone
}
