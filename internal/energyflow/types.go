// Package energyflow computes the four directional energy flows of a
// PV+ESS+Grid+Load site (pv_to_ess, grid_to_ess, ess_to_load,
// ess_to_grid) from the cumulative kWh counters published by Huawei
// SmartLogger devices over Modbus.
//
// The package exposes two stateless functions:
//
//   - Allocate runs the allocation rule on a single (prev, curr)
//     pair of merged accumulator snapshots and returns the four flow
//     kWh deltas for that interval plus a diagnostic Result.
//   - Recompute takes a slice of role-tagged RawSamples for an
//     arbitrary window, buckets them on AllocationWindowSeconds, and
//     iterates Allocate across consecutive merged buckets. The four
//     flow totals are the sum of all non-skipped intervals.
//
// Neither function holds state across calls: the API server invokes
// Recompute on the raw telemetry_samples rows for each
// /energy-summary request, so historical periods always reproduce
// byte-identical numbers and there is nothing to "reseed" on a
// process restart.
package energyflow

import "time"

// Sample is one logical snapshot fed to Allocate. Pointer fields are
// optional: nil means the value is unavailable for this snapshot.
// Allocate requires the five accumulator fields below; the
// instantaneous powers are kept for diagnostics, fallback paths and
// the ESS sign sanity check.
type Sample struct {
	Timestamp time.Time

	// Instantaneous powers (kW). Used for diagnostics, sign of
	// ESS check, and fallback when an accumulator is briefly
	// unavailable. Not part of the main delta calculation.
	PVPowerKw   *float64
	ESSPowerKw  *float64
	LoadPowerKw *float64
	GridPowerKw *float64
	SOCPercent  *float64

	// Cumulative accumulators (kWh). Allocate computes deltas
	// between two snapshots and runs the directional allocation
	// rule on those deltas.
	AccumulatedPVYieldKwh          *float64
	AccumulatedPurchasedKwh        *float64
	AccumulatedSoldKwh             *float64
	AccumulatedPowerConsumptionKwh *float64
	TotalESSChargedKwh             *float64
	TotalESSDischargedKwh          *float64
}

// Result holds the four directional flow deltas (kWh) for an
// interval plus diagnostics. Skipped == true means the interval was
// rejected (negative delta, dt out of range, missing accumulator,
// non-finite) and the four flow values are zero.
type Result struct {
	PVToESSKwh   float64
	GridToESSKwh float64
	ESSToLoadKwh float64
	ESSToGridKwh float64

	// EssChargedKwh / EssDischargedKwh are the per-interval ESS
	// counter deltas, exposed so callers can re-run the balance
	// check (spec §Перевірка балансу) without recomputing.
	EssChargedKwh    float64
	EssDischargedKwh float64

	Skipped  bool
	Warnings []string
}

// Topology classifies a site as either a single SmartLogger covering
// both PV and ESS (`single_smartlogger`) or two SmartLoggers split by
// role (`dual_smartlogger`). The collector detects the topology
// automatically from the metric_keys whitelists declared on each
// Modbus device — operators do not configure it explicitly.
type Topology string

const (
	TopologySingle Topology = "single_smartlogger"
	TopologyDual   Topology = "dual_smartlogger"
)

// DefaultMaxEssPowerKw is the fleet-wide ESS charge/discharge power
// ceiling (kW) the on-the-fly compute applies when an organization has
// no explicit ess_max_power_kw override. It sits far above any real
// site's rated ESS power (elevator BESS are well under 2 MW) yet far
// below the implausible counter-step spikes the guard exists to catch
// (a single re-based counter implies hundreds of MW over one bucket).
// Allocate stays opt-in: the library never applies this on its own —
// only the API call sites inject it so unit tests keep their explicit
// Options.
const DefaultMaxEssPowerKw = 10000

// Role identifies which logical role a Modbus device plays in the
// energy-flow calculation. Roles are auto-detected from the device's
// resolved metric_keys.
type Role string

const (
	RoleNone   Role = ""       // device contributes nothing usable
	RolePV     Role = "pv"     // PV SmartLogger (dual topology)
	RoleESS    Role = "ess"    // ESS SmartLogger (dual topology)
	RoleSingle Role = "single" // single SmartLogger covering both
)

// Options tunes the allocation algorithm. The zero value of each
// field means "use spec default"; WithDefaults applies them.
type Options struct {
	Topology                  Topology
	EssDischargeSign          int     // 1 (default) or -1
	PollIntervalSeconds       int     // default 1
	AllocationWindowSeconds   int     // default 60
	MaxGapSeconds             int     // default 5
	WarnDeviceTimeSkewSeconds int     // default 5
	BalanceToleranceKwh       float64 // default 0.1
	ActivePvPowerAddress      int     // default 440388

	// MaxEssPowerKw is the physical charge/discharge power ceiling of
	// the site's ESS in kW. When > 0, Allocate rejects any interval
	// whose implied average ESS power (delta_ess_charged or
	// delta_ess_discharged over dt) exceeds this bound — that signals a
	// cumulative-counter step (device re-base / firmware resync /
	// corrupted high reading) rather than real energy. 0 disables the
	// guard. No spec default: a wrong value would clip real data, so it
	// is opt-in per organization.
	MaxEssPowerKw float64
}

// DefaultOptions returns the spec-recommended defaults.
func DefaultOptions() Options {
	return Options{
		Topology:                  TopologyDual,
		EssDischargeSign:          1,
		PollIntervalSeconds:       1,
		AllocationWindowSeconds:   60,
		MaxGapSeconds:             5,
		WarnDeviceTimeSkewSeconds: 5,
		BalanceToleranceKwh:       0.1,
		ActivePvPowerAddress:      440388,
	}
}

// WithDefaults returns a copy of o with zero-valued fields replaced
// by spec defaults. Non-zero fields are kept verbatim. EssDischargeSign
// keeps its value only when it is exactly +1 or -1; any other input
// (including the zero value) collapses to the +1 default.
func (o Options) WithDefaults() Options {
	d := DefaultOptions()
	if o.Topology != "" {
		d.Topology = o.Topology
	}
	if o.EssDischargeSign == 1 || o.EssDischargeSign == -1 {
		d.EssDischargeSign = o.EssDischargeSign
	}
	if o.PollIntervalSeconds > 0 {
		d.PollIntervalSeconds = o.PollIntervalSeconds
	}
	if o.AllocationWindowSeconds > 0 {
		d.AllocationWindowSeconds = o.AllocationWindowSeconds
	}
	if o.MaxGapSeconds > 0 {
		d.MaxGapSeconds = o.MaxGapSeconds
	}
	if o.WarnDeviceTimeSkewSeconds > 0 {
		d.WarnDeviceTimeSkewSeconds = o.WarnDeviceTimeSkewSeconds
	}
	if o.BalanceToleranceKwh > 0 {
		d.BalanceToleranceKwh = o.BalanceToleranceKwh
	}
	if o.ActivePvPowerAddress > 0 {
		d.ActivePvPowerAddress = o.ActivePvPowerAddress
	}
	if o.MaxEssPowerKw > 0 {
		d.MaxEssPowerKw = o.MaxEssPowerKw
	}
	return d
}
