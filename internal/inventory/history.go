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
// produce no events. Output lists are newest-first for the UI.
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

	prev := ordered[0]
	for i := 1; i < len(ordered); i++ {
		cur := ordered[i]
		for _, key := range AllMetricKeys {
			from := fieldValue(prev, key)
			to := fieldValue(cur, key)
			if !fieldChanged(key, from, to) {
				continue
			}
			out[key] = append(out[key], FieldChange{
				At:         cur.Time.UTC(),
				From:       cloneFloatPtr(from),
				To:         cloneFloatPtr(to),
				PollReason: cur.PollReason,
			})
		}
		prev = cur
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

func fieldChanged(key string, from, to *float64) bool {
	if from == nil && to == nil {
		return false
	}
	if from == nil || to == nil {
		return true
	}
	return math.Abs(*from-*to) > fieldTolerance(key)
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
