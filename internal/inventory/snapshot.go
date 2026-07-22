package inventory

import (
	"math"
	"sort"
	"time"

	"github.com/nesh/sestelemetry/internal/config"
)

// Poll reasons recorded on each snapshot row.
const (
	PollReasonStartup = "startup"
	PollReasonHourly  = "hourly"
	PollReasonDaily   = "daily"
)

// Quality flags (handoff §0 / §3 / §5).
const (
	FlagControlModeNotRemote = "CONTROL_MODE_NOT_REMOTE"
	FlagInventoryMismatch    = "INVENTORY_MISMATCH"
	FlagControlModeDisagree  = "CONTROL_MODE_DISAGREE"
	FlagModbusError          = "MODBUS_ERROR"
)

// Required remote scheduling mode for third-party EMS writes.
const ActivePowerControlModeRemote = 4

// Snapshot is one plant-inventory observation (site-level after merge).
type Snapshot struct {
	Time           time.Time
	OrganizationID string
	DeviceHost     string // empty for merged multi-SL site snapshot
	PollReason     string

	PVRatedKw              *float64
	ESSRatedKw             *float64
	ESSRatedKwh            *float64
	ESSCount               *float64
	PCSCount               *float64
	ESSSOHPct              *float64
	ActivePowerControlMode *float64

	QualityFlags []string
	Raw          map[string]any
}

// DeviceReading is the decoded inventory values from one SmartLogger.
type DeviceReading struct {
	Host                   string
	PVRatedKw              *float64
	ESSRatedKw             *float64
	ESSRatedKwh            *float64
	ESSCount               *float64
	PCSCount               *float64
	ESSSOHPct              *float64
	ActivePowerControlMode *float64
	RawRegisters           map[string]float64
	Err                    error
}

// Merge combines per-device readings into one site snapshot. PV fields
// come from the PV (or single) logger; ESS fields from the ESS (or
// single) logger. Dual-SL mode disagreement is flagged.
func Merge(orgID, pollReason string, ts time.Time, readings []DeviceReading, expected *config.InventoryExpected) Snapshot {
	snap := Snapshot{
		Time:           ts.UTC(),
		OrganizationID: orgID,
		PollReason:     pollReason,
		Raw:            map[string]any{},
	}

	sources := map[string]string{}
	var modes []float64
	flags := map[string]struct{}{}

	for _, r := range readings {
		if r.Err != nil {
			flags[FlagModbusError] = struct{}{}
			snap.Raw["error_"+r.Host] = r.Err.Error()
			continue
		}
		if r.Host != "" {
			if len(readings) == 1 {
				snap.DeviceHost = r.Host
			}
			sources[r.Host] = "ok"
		}
		assignIfSet(&snap.PVRatedKw, r.PVRatedKw)
		assignIfSet(&snap.ESSRatedKw, r.ESSRatedKw)
		assignIfSet(&snap.ESSRatedKwh, r.ESSRatedKwh)
		assignIfSet(&snap.ESSCount, r.ESSCount)
		assignIfSet(&snap.PCSCount, r.PCSCount)
		assignIfSet(&snap.ESSSOHPct, r.ESSSOHPct)
		if r.ActivePowerControlMode != nil {
			modes = append(modes, *r.ActivePowerControlMode)
			assignIfSet(&snap.ActivePowerControlMode, r.ActivePowerControlMode)
		}
		for k, v := range r.RawRegisters {
			key := k
			if r.Host != "" && len(readings) > 1 {
				key = r.Host + "." + k
			}
			snap.Raw[key] = v
		}
	}
	if len(sources) > 0 {
		snap.Raw["sources"] = sources
	}

	if len(modes) > 1 {
		first := modes[0]
		for _, m := range modes[1:] {
			if m != first {
				flags[FlagControlModeDisagree] = struct{}{}
				break
			}
		}
	}
	if snap.ActivePowerControlMode != nil && *snap.ActivePowerControlMode != ActivePowerControlModeRemote {
		flags[FlagControlModeNotRemote] = struct{}{}
	}

	if mismatch := CompareExpected(snap, expected); len(mismatch) > 0 {
		flags[FlagInventoryMismatch] = struct{}{}
		snap.Raw["mismatch"] = mismatch
	}

	snap.QualityFlags = sortedKeys(flags)
	return snap
}

func assignIfSet(dst **float64, src *float64) {
	if src == nil {
		return
	}
	v := *src
	*dst = &v
}

// CompareExpected returns a map of field → {expected, actual} for every
// configured passport field that differs from the snapshot.
func CompareExpected(snap Snapshot, expected *config.InventoryExpected) map[string]map[string]float64 {
	if expected == nil {
		return nil
	}
	out := map[string]map[string]float64{}
	check := func(name string, want *float64, got *float64, tol float64) {
		if want == nil || got == nil {
			return
		}
		if math.Abs(*want-*got) > tol {
			out[name] = map[string]float64{"expected": *want, "actual": *got}
		}
	}
	check(MetricPVRatedKw, expected.PVRatedKw, snap.PVRatedKw, 0.5)
	check(MetricESSRatedKw, expected.ESSRatedKw, snap.ESSRatedKw, 0.5)
	check(MetricESSRatedKwh, expected.ESSRatedKwh, snap.ESSRatedKwh, 0.5)
	check(MetricESSCount, expected.ESSCount, snap.ESSCount, 0.1)
	if len(out) == 0 {
		return nil
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ReadingFromValues builds a DeviceReading from metric_key → decoded value.
func ReadingFromValues(host string, values map[string]float64) DeviceReading {
	r := DeviceReading{
		Host:         host,
		RawRegisters: values,
	}
	if v, ok := values[MetricPVRatedKw]; ok {
		r.PVRatedKw = floatPtr(v)
	}
	if v, ok := values[MetricESSRatedKw]; ok {
		r.ESSRatedKw = floatPtr(v)
	}
	if v, ok := values[MetricESSRatedKwh]; ok {
		r.ESSRatedKwh = floatPtr(v)
	}
	if v, ok := values[MetricESSCount]; ok {
		r.ESSCount = floatPtr(v)
	}
	if v, ok := values[MetricPCSCount]; ok {
		r.PCSCount = floatPtr(v)
	}
	if v, ok := values[MetricESSSOHPct]; ok {
		r.ESSSOHPct = floatPtr(v)
	}
	if v, ok := values[MetricActivePowerControlMode]; ok {
		r.ActivePowerControlMode = floatPtr(v)
	}
	return r
}

func floatPtr(v float64) *float64 { return &v }
