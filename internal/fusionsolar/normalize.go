package fusionsolar

// metricKeyByLoggerField maps a FusionSolar SmartLogger (devTypeId=63)
// cumulative field to the telemetry_samples metric_key the live Modbus
// collector writes for the same quantity. Values are written verbatim
// (they are already lifetime kWh counters), so the read-side delta /
// flow logic treats archive and live data identically.
//
//	total_yield              -> accumulated_pv_energy_yield_kwh
//	total_power_consumption  -> accumulated_power_consumption_kwh
//	total_supply_from_grid   -> accumulated_electricity_purchased_kwh (grid import)
//	total_feed_in_to_grid    -> accumulated_electricity_sold_kwh      (grid export)
//	total_charge             -> total_energy_charged_kwh
//	total_discharge          -> total_energy_discharged_kwh
var metricKeyByLoggerField = map[string]string{
	"total_yield":             "accumulated_pv_energy_yield_kwh",
	"total_power_consumption": "accumulated_power_consumption_kwh",
	"total_supply_from_grid":  "accumulated_electricity_purchased_kwh",
	"total_feed_in_to_grid":   "accumulated_electricity_sold_kwh",
	"total_charge":            "total_energy_charged_kwh",
	"total_discharge":         "total_energy_discharged_kwh",
}

// essChargeFields / essDischargeFields are the SmartLogger ESS counter
// fields, in preference order. ac_total_* is the AC-side counter Huawei
// exposes when the plain total_* field is absent.
var (
	essChargeFields    = []string{"total_charge", "ac_total_charge_energy"}
	essDischargeFields = []string{"total_discharge", "ac_total_discharge_energy"}
)

const (
	// socMetricKey is the dashboard battery state-of-charge metric.
	socMetricKey = "soc_percent"
	// essSocField is the per-pack SOC field on devTypeId=41 devices.
	essSocField = "battery_soc"

	// SourceLabel / SourceValue tag every imported row so it is
	// distinguishable from live Modbus samples. The idempotency delete
	// is scoped to this tag, so a re-import only ever rewrites its own
	// archive rows and never touches real data — and an operator can
	// wipe + re-pull the archive cleanly at any time.
	SourceLabel = "source"
	SourceValue = "fusionsolar"
)

// importableMetricKeys is the full set of metric_keys this importer can
// write. Used to scope the idempotency delete so a re-import only
// rewrites the archive-owned counters and never touches unrelated
// metrics the live collector may have stored in the same window.
var importableMetricKeys = func() []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(metricKeyByLoggerField)+3)
	add := func(k string) {
		if _, ok := seen[k]; ok {
			return
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	for _, v := range metricKeyByLoggerField {
		add(v)
	}
	add("total_energy_charged_kwh")
	add("total_energy_discharged_kwh")
	add(socMetricKey)
	return out
}()

// ImportableMetricKeys returns a copy of the metric_keys the importer
// writes, used by the idempotency delete.
func ImportableMetricKeys() []string {
	out := make([]string, len(importableMetricKeys))
	copy(out, importableMetricKeys)
	return out
}
