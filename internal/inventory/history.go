package inventory

import (
	"math"
	"time"
)

// FieldChange is one detected change of a passport/ops field between
// consecutive snapshots.
type FieldChange struct {
	At         time.Time `json:"at"`
	From       *float64  `json:"from"`
	To         *float64  `json:"to"`
	PollReason string    `json:"poll_reason,omitempty"`
}

// DiffHistory walks snapshots in chronological order (oldest → newest)
// and emits a change event whenever a tracked field's value differs from
// the previous snapshot beyond a small tolerance. Identical hourly polls
// produce no events.
//
// Missing readings never produce events: a nil (failed Modbus read) or a
// zero on a nameplate field (SmartLogger aggregates rated power / device
// counts over ONLINE devices only, so overnight it reports 0 while the
// inverters sleep) is treated as "not reported", and the previous real
// value is carried forward for comparison. Only real-value → real-value
// transitions appear in the history. Output lists are newest-first.
func DiffHistory(snapshots []Snapshot) map[string][]FieldChange {
	out := make(map[string][]FieldChange, len(AllMetricKeys))
	for _, k := range AllMetricKeys {
		out[k] = nil
	}
	if len(snapshots) < 2 {
		return out
	}

	// Caller may pass DESC from DB; normalize to ASC for comparison.
	ordered := make([]Snapshot, len(snapshots))
	copy(ordered, snapshots)
	if ordered[0].Time.After(ordered[len(ordered)-1].Time) {
		for i, j := 0, len(ordered)-1; i < j; i, j = i+1, j-1 {
			ordered[i], ordered[j] = ordered[j], ordered[i]
		}
	}

	// lastReal carries the most recent real (non-missing) value per field
	// across gaps, so a nil/0 night snapshot between two real readings
	// doesn't fabricate 550→0→550 style events.
	lastReal := make(map[string]*float64, len(AllMetricKeys))
	for _, key := range AllMetricKeys {
		lastReal[key] = NormalizeFieldValue(key, fieldValue(ordered[0], key))
	}
	for i := 1; i < len(ordered); i++ {
		cur := ordered[i]
		for _, key := range AllMetricKeys {
			to := NormalizeFieldValue(key, fieldValue(cur, key))
			if to == nil {
				continue // missing reading: keep waiting for a real value
			}
			from := lastReal[key]
			if from != nil && math.Abs(*from-*to) > fieldTolerance(key) {
				out[key] = append(out[key], FieldChange{
					At:         cur.Time.UTC(),
					From:       cloneFloatPtr(from),
					To:         cloneFloatPtr(to),
					PollReason: cur.PollReason,
				})
			}
			lastReal[key] = to
		}
	}

	// Newest first for the UI timeline.
	for key, events := range out {
		for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
			events[i], events[j] = events[j], events[i]
		}
		out[key] = events
	}
	return out
}

func fieldValue(s Snapshot, key string) *float64 {
	switch key {
	case MetricPVRatedKw:
		return s.PVRatedKw
	case MetricESSRatedKw:
		return s.ESSRatedKw
	case MetricESSRatedKwh:
		return s.ESSRatedKwh
	case MetricESSCount:
		return s.ESSCount
	case MetricPCSCount:
		return s.PCSCount
	case MetricESSSOHPct:
		return s.ESSSOHPct
	case MetricActivePowerControlMode:
		return s.ActivePowerControlMode
	default:
		return nil
	}
}

// NormalizeFieldValue maps "not really reported" readings to nil. Nil
// stays nil (failed Modbus read); a zero on nameplate fields also becomes
// nil because the SmartLogger aggregates rated power, capacity, device
// counts and SOH over online devices only — 0 means "nothing connected
// right now" (e.g. inverters asleep overnight), never a real passport
// value. Control mode keeps 0 as-is: it is a valid enum (no restriction).
func NormalizeFieldValue(key string, v *float64) *float64 {
	if v == nil {
		return nil
	}
	if *v == 0 && zeroIsMissing(key) {
		return nil
	}
	return v
}

func zeroIsMissing(key string) bool {
	switch key {
	case MetricPVRatedKw, MetricESSRatedKw, MetricESSRatedKwh,
		MetricESSCount, MetricPCSCount, MetricESSSOHPct:
		return true
	default:
		return false
	}
}

func fieldTolerance(key string) float64 {
	switch key {
	case MetricPVRatedKw, MetricESSRatedKw, MetricESSRatedKwh:
		return 0.5
	case MetricESSCount, MetricPCSCount, MetricESSSOHPct, MetricActivePowerControlMode:
		return 0.1
	default:
		return 0.01
	}
}

func cloneFloatPtr(v *float64) *float64 {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}
