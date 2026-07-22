// Package inventory implements the rare plant-passport Modbus poll
// (rated PV/ESS, cabinet counts, SOH, active power control mode).
// These registers must not enter the 1s telemetry_samples stream.
package inventory

import "github.com/nesh/sestelemetry/internal/energyflow"

// Metric keys for the plant inventory set (Issue 52 / handoff 2026-07-21).
const (
	MetricPVRatedKw              = "pv_rated_kw"
	MetricESSRatedKw             = "ess_rated_kw"
	MetricESSRatedKwh            = "ess_rated_kwh"
	MetricESSCount               = "ess_count"
	MetricPCSCount               = "pcs_count"
	MetricESSSOHPct              = "ess_soh_pct"
	MetricActivePowerControlMode = "active_power_control_mode"
)

// AllMetricKeys is the full inventory set in stable catalog order.
var AllMetricKeys = []string{
	MetricPVRatedKw,
	MetricESSRatedKw,
	MetricESSRatedKwh,
	MetricESSCount,
	MetricPCSCount,
	MetricESSSOHPct,
	MetricActivePowerControlMode,
}

var inventoryKeySet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(AllMetricKeys))
	for _, k := range AllMetricKeys {
		m[k] = struct{}{}
	}
	return m
}()

// IsInventoryMetricKey reports whether key belongs to the rare inventory
// set and must be excluded from the high-frequency telemetry poll.
func IsInventoryMetricKey(key string) bool {
	_, ok := inventoryKeySet[key]
	return ok
}

// KeysForRole returns the inventory registers to read from a device of
// the given energy-flow role (PV SL vs ESS SL vs single SmartLogger).
func KeysForRole(role energyflow.Role) []string {
	switch role {
	case energyflow.RolePV:
		return []string{MetricPVRatedKw, MetricActivePowerControlMode}
	case energyflow.RoleESS:
		return []string{
			MetricESSRatedKw,
			MetricESSRatedKwh,
			MetricESSCount,
			MetricPCSCount,
			MetricESSSOHPct,
			MetricActivePowerControlMode,
		}
	default:
		// RoleSingle / RoleNone: full plant passport on one box.
		out := make([]string, len(AllMetricKeys))
		copy(out, AllMetricKeys)
		return out
	}
}
