package energyflow

import (
	"sort"
	"time"
)

// RawSample is one logical poll snapshot reconstructed from
// telemetry_samples. Values is the engineering-unit map of source
// metric_keys for whichever of the SrcXxx fields this snapshot
// carries; the caller is expected to fold rows that share the same
// (time, device_host) tuple into a single RawSample before handing
// the slice to Recompute.
type RawSample struct {
	Time   time.Time
	Role   Role
	Values map[string]float64
}

// RecomputeResult is the period summary produced by one Recompute
// pass over a slice of raw rows.
//
// Totals are the four kWh deltas attributed to the period (each one
// a sum of all non-skipped Allocate calls). The remaining counters
// are diagnostics for API logs / tests: how many buckets the run
// considered, how many were dropped for missing role coverage, how
// many intervals Allocate processed vs skipped, and a bounded tail
// of the warnings the underlying Allocate calls emitted.
type RecomputeResult struct {
	Totals             map[string]float64
	ProcessedIntervals int
	SkippedIntervals   int
	BucketsConsidered  int
	BucketsDropped     int
	Warnings           []string
}

// Recompute runs the stateless allocation rule over a time-ordered
// slice of role-tagged raw rows and returns the period flow totals.
//
// rows must be sorted by Time ASC; ties are accepted (multiple
// metrics at the same instant from the same device).
//
// The function:
//   - filters SmartLogger UINT32 sentinel readings (against the gain
//     ladder used by the Huawei catalog) so a stale 42949672.95
//     inside a historical range does not poison a delta;
//   - groups rows into buckets of opts.AllocationWindowSeconds
//     (default 60 s) and keeps the latest reading per role inside
//     each bucket;
//   - merges PV and ESS role snapshots into one Sample per bucket
//     using the shared mergeAccumulators / mergePowers helpers;
//   - runs Allocate over consecutive merged Samples; skipped
//     intervals are counted into SkippedIntervals with their reasons
//     joined into Warnings (bounded tail);
//   - sums the four flow kWh deltas into res.Totals.
//
// opts.MaxGapSeconds is preserved verbatim; pass 0 to disable the
// gap guard (recommended for historical compute where collector
// outages are normal). All other Options fields fall back to spec
// defaults.
func Recompute(rows []RawSample, opts Options) RecomputeResult {
	// Historical compute must tolerate hours-long gaps caused by
	// collector outages, so callers who pass MaxGapSeconds=0 actually
	// mean "disable the guard entirely". Allocate() re-runs
	// WithDefaults() internally and would otherwise rewrite a zero
	// back to the spec default (5 s), erasing every real counter
	// delta that accumulated across an outage. Translate the zero
	// into a very large explicit cap that survives WithDefaults
	// and is effectively unlimited for any realistic window.
	const noGapCap = 365 * 24 * 3600
	if opts.MaxGapSeconds == 0 {
		opts.MaxGapSeconds = noGapCap
	}
	opts = opts.WithDefaults()
	allocOpts := opts

	res := RecomputeResult{
		Totals: make(map[string]float64, len(SyntheticMetricKeys)),
	}
	for _, k := range SyntheticMetricKeys {
		res.Totals[k] = 0
	}

	if len(rows) < 2 {
		return res
	}

	windowSec := opts.AllocationWindowSeconds
	if windowSec <= 0 {
		windowSec = 60
	}
	bucketDur := time.Duration(windowSec) * time.Second

	type bucket struct {
		end    time.Time
		latest map[Role]*roleSnapshot
	}
	bucketsByEnd := make(map[int64]*bucket)
	bucketEnds := make([]int64, 0, len(rows)/3+1)

	for _, row := range rows {
		if row.Role == RoleNone || len(row.Values) == 0 {
			continue
		}
		clean := cleanRawValues(row.Values)
		if len(clean) == 0 {
			continue
		}
		end := bucketEnd(row.Time, bucketDur)
		key := end.UnixNano()
		b, ok := bucketsByEnd[key]
		if !ok {
			b = &bucket{end: end, latest: make(map[Role]*roleSnapshot)}
			bucketsByEnd[key] = b
			bucketEnds = append(bucketEnds, key)
		}
		existing, ok := b.latest[row.Role]
		if !ok || !row.Time.Before(existing.timestamp) {
			merged := make(map[string]float64, len(clean))
			if ok {
				for k, v := range existing.values {
					merged[k] = v
				}
			}
			for k, v := range clean {
				merged[k] = v
			}
			b.latest[row.Role] = &roleSnapshot{
				timestamp: row.Time,
				values:    merged,
			}
		} else {
			for k, v := range clean {
				if _, present := existing.values[k]; !present {
					existing.values[k] = v
				}
			}
		}
	}

	if len(bucketEnds) < 2 {
		res.BucketsConsidered = len(bucketEnds)
		return res
	}
	sort.Slice(bucketEnds, func(i, j int) bool { return bucketEnds[i] < bucketEnds[j] })

	const maxWarnings = 32
	appendWarning := func(s string) {
		if len(res.Warnings) >= maxWarnings {
			return
		}
		res.Warnings = append(res.Warnings, s)
	}

	var prev *Sample
	for _, key := range bucketEnds {
		b := bucketsByEnd[key]
		res.BucketsConsidered++
		sample, ok := mergeBucket(b.latest)
		if !ok {
			res.BucketsDropped++
			continue
		}
		if prev == nil {
			prev = &Sample{}
			*prev = sample
			continue
		}

		out := Allocate(*prev, sample, allocOpts)
		for _, w := range out.Warnings {
			appendWarning(w)
		}
		if out.Skipped {
			res.SkippedIntervals++
			next := sample
			prev = &next
			continue
		}
		res.ProcessedIntervals++
		res.Totals[MetricPVToESSKwh] += out.PVToESSKwh
		res.Totals[MetricGridToESSKwh] += out.GridToESSKwh
		res.Totals[MetricESSToLoadKwh] += out.ESSToLoadKwh
		res.Totals[MetricESSToGridKwh] += out.ESSToGridKwh

		next := sample
		prev = &next
	}

	return res
}

// bucketEnd returns the right-closed end of the bucket of width
// `dur` containing `t` (i.e. ceil(t / dur) * dur). UTC-aligned so the
// bucketing is timezone-independent — identical inputs replay
// identical boundaries regardless of the API server's local TZ.
func bucketEnd(t time.Time, dur time.Duration) time.Time {
	if dur <= 0 {
		return t.UTC()
	}
	u := t.UTC()
	n := u.UnixNano()
	step := dur.Nanoseconds()
	// Right-closed: a row exactly on a boundary belongs to the bucket
	// that ends at the boundary, not the next one. This matches the
	// API summary's `time <= $to` lookup.
	idx := n / step
	if n%step != 0 {
		idx++
	}
	if idx == 0 {
		idx = 1
	}
	return time.Unix(0, idx*step).UTC()
}

// cleanRawValues applies the SmartLogger UINT32 sentinel / non-finite
// filter so an overflow value such as 0xFFFFFFFF * gain does not
// poison a delta. The filter scans the three gains present in the
// Huawei catalogue (0.001, 0.01, 0.1); a sentinel hit drops the
// metric from the bucket but keeps the rest of the row.
func cleanRawValues(in map[string]float64) map[string]float64 {
	out := make(map[string]float64, len(in))
	for k, v := range in {
		if !isFiniteFloat(v) {
			continue
		}
		if IsInvalidUint32Scaled(v, 0.01) ||
			IsInvalidUint32Scaled(v, 0.001) ||
			IsInvalidUint32Scaled(v, 0.1) {
			continue
		}
		out[k] = v
	}
	return out
}

// mergeBucket folds the latest-per-role readings of one bucket into a
// single Sample. Returns ok=false when the bucket has neither a
// RoleSingle nor a (RolePV + RoleESS) pair — Allocate requires the
// full set of five accumulators, so a half-populated bucket is
// dropped rather than fed in.
func mergeBucket(latest map[Role]*roleSnapshot) (Sample, bool) {
	if s, ok := latest[RoleSingle]; ok {
		merged := Sample{Timestamp: s.timestamp}
		mergeAccumulators(&merged, s.values)
		mergePowers(&merged, s.values)
		return merged, true
	}
	pv, pvOK := latest[RolePV]
	ess, essOK := latest[RoleESS]
	if !pvOK || !essOK {
		return Sample{}, false
	}
	merged := Sample{Timestamp: laterOf(pv.timestamp, ess.timestamp)}
	mergeAccumulators(&merged, pv.values)
	mergeAccumulators(&merged, ess.values)
	mergePowers(&merged, pv.values)
	mergePowers(&merged, ess.values)
	return merged, true
}
