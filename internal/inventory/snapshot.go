package inventory

import (
	"sort"
	"time"
)

// Poll reasons recorded on each snapshot row.
const (
	PollReasonStartup = "startup"
	PollReasonHourly  = "hourly"
	PollReasonDaily   = "daily"
)

// Quality flags derived only from controller (Modbus) readings.
const (
	FlagControlModeNotRemote = "CONTROL_MODE_NOT_REMOTE"
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
func Merge(orgID, pollReason string, ts time.Time, readings []DeviceReading) Snapshot {
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

	snap.QualityFlags = sortedKeys(flags)
	return snap
}

// CoalesceLatest builds the "current passport" view from snapshots
// (newest first, as returned by storage). Metadata comes from the newest
// snapshot, but each metric holds the most recent REAL value: nils
// (failed reads) and zeros on nameplate fields (SmartLogger reports 0
// while devices are offline, e.g. inverters overnight) are skipped and
// backfilled from older snapshots. ok is false when the list is empty.
func CoalesceLatest(snapshots []Snapshot) (Snapshot, bool) {
	if len(snapshots) == 0 {
		return Snapshot{}, false
	}
	// Ensure newest-first ordering regardless of caller.
	ordered := make([]Snapshot, len(snapshots))
	copy(ordered, snapshots)
	if len(ordered) > 1 && ordered[0].Time.Before(ordered[len(ordered)-1].Time) {
		for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
			ordered[i], ordered[j] = ordered[j], ordered[i]
		}
	}

	out := ordered[0]
	for _, key := range AllMetricKeys {
		var val *float64
		for _, s := range ordered {
			if v := NormalizeFieldValue(key, fieldValue(s, key)); v != nil {
				val = cloneFloatPtr(v)
				break
			}
		}
		setFieldValue(&out, key, val)
	}
	return out, true
}

func setFieldValue(s *Snapshot, key string, v *float64) {
	switch key {
	case MetricPVRatedKw:
		s.PVRatedKw = v
	case MetricESSRatedKw:
		s.ESSRatedKw = v
	case MetricESSRatedKwh:
		s.ESSRatedKwh = v
	case MetricESSCount:
		s.ESSCount = v
	case MetricPCSCount:
		s.PCSCount = v
	case MetricESSSOHPct:
		s.ESSSOHPct = v
	case MetricActivePowerControlMode:
		s.ActivePowerControlMode = v
	}
}

func assignIfSet(dst **float64, src *float64) {
	if src == nil {
		return
	}
	v := *src
	*dst = &v
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
