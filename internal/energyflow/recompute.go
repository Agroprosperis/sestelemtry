package energyflow

import (
	"fmt"
	"sort"
	"time"
)

// RawSample is one row read back from telemetry_samples that the
// backfill pipeline classifies into a role and then merges with other
// rows that share its bucket into a logical poll snapshot. Values is
// the decoded engineering-unit map (metric_key → value) for whichever
// of the SrcXxx keys this row carries; the backfill loader unions the
// per-metric rows of the same (time, device_host) tuple into a single
// RawSample before handing the slice to Recompute.
type RawSample struct {
	Time   time.Time
	Role   Role
	Values map[string]float64
}

// RecomputeResult is the period summary produced by a backfill run.
//
// Totals are the four kWh deltas attributed to the period (each one a
// sum of all non-skipped Allocate calls). Emitted carries one
// cumulative sample per metric per merged-bucket boundary so the
// caller can persist them and the existing last(end)-last(seed)
// summary query keeps working over the recomputed range. The cumulative
// values start from the seed handed to Recompute and grow monotonically
// — re-running for the same period with the same seed reproduces the
// same emitted timeline byte-for-byte.
type RecomputeResult struct {
	Totals             map[string]float64
	Emitted            []EmittedSample
	ProcessedIntervals int
	SkippedIntervals   int
	BucketsConsidered  int
	BucketsDropped     int
	Warnings           []string
}

// Recompute runs the allocation rule over a time-ordered slice of
// role-tagged raw rows and returns both the period totals and the
// per-bucket cumulative samples that the caller should upsert.
//
// rows must be sorted by Time ASC; ties are accepted (multiple metrics
// at the same instant from the same device). seed is the cumulative
// starting value for each synthetic metric — pass the latest synthetic
// sample strictly before the recompute window so the emitted timeline
// continues the live aggregator's totals. nil/empty seed treats the
// run as if the meters started from zero at the first bucket.
//
// The function:
//   - filters SmartLogger UINT32 sentinel readings (same gain ladder as
//     Aggregator.Submit) so a stale 42949672.95 inside a historical
//     range does not poison a delta;
//   - groups rows into buckets of opts.AllocationWindowSeconds (default
//     60 s) and keeps the latest reading per role inside each bucket;
//   - merges PV and ESS role snapshots into one Sample per bucket via
//     the same mergeAccumulators / mergePowers helpers used by the
//     live aggregator;
//   - runs Allocate over consecutive merged Samples; skipped intervals
//     are counted into SkippedIntervals with their reason joined into
//     Warnings (bounded tail);
//   - accumulates the four flow kWh deltas into running cumulative
//     totals on top of seed and emits one EmittedSample per metric per
//     bucket whose interval did not skip.
//
// opts.MaxGapSeconds is preserved verbatim; pass 0 to disable the gap
// guard (recommended for historical backfill where collector outages
// are normal). All other Options fields fall back to spec defaults.
func Recompute(rows []RawSample, seed map[string]float64, opts Options) RecomputeResult {
	// Historical backfill must tolerate hours-long gaps caused by
	// collector outages, so callers who pass MaxGapSeconds=0 actually
	// mean "disable the guard entirely". Allocate() re-runs
	// WithDefaults() internally and would otherwise rewrite a zero
	// back to the spec default (5 s), erasing every real counter
	// delta that accumulated across an outage. We translate the
	// zero into a very large explicit cap that survives WithDefaults
	// and is effectively unlimited for any realistic recompute
	// window (one year of seconds).
	const noGapCap = 365 * 24 * 3600
	if opts.MaxGapSeconds == 0 {
		opts.MaxGapSeconds = noGapCap
	}
	opts = opts.WithDefaults()
	allocOpts := opts

	res := RecomputeResult{
		Totals:  make(map[string]float64, len(SyntheticMetricKeys)),
		Emitted: nil,
	}
	for _, k := range SyntheticMetricKeys {
		res.Totals[k] = 0
	}
	cumulative := make(map[string]float64, len(SyntheticMetricKeys))
	for _, k := range SyntheticMetricKeys {
		if seed != nil {
			if v, ok := seed[k]; ok && isFiniteFloat(v) && v >= 0 {
				cumulative[k] = v
			}
		}
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
		cumulative[MetricPVToESSKwh] += out.PVToESSKwh
		cumulative[MetricGridToESSKwh] += out.GridToESSKwh
		cumulative[MetricESSToLoadKwh] += out.ESSToLoadKwh
		cumulative[MetricESSToGridKwh] += out.ESSToGridKwh
		res.Totals[MetricPVToESSKwh] += out.PVToESSKwh
		res.Totals[MetricGridToESSKwh] += out.GridToESSKwh
		res.Totals[MetricESSToLoadKwh] += out.ESSToLoadKwh
		res.Totals[MetricESSToGridKwh] += out.ESSToGridKwh

		for _, k := range SyntheticMetricKeys {
			res.Emitted = append(res.Emitted, EmittedSample{
				Time:      sample.Timestamp,
				MetricKey: k,
				Value:     cumulative[k],
			})
		}

		next := sample
		prev = &next
	}

	return res
}

// bucketEnd returns the right-closed end of the bucket of width
// `dur` containing `t` (i.e. ceil(t / dur) * dur). UTC-aligned so the
// bucketing is timezone-independent — identical seeds replay identical
// boundaries regardless of the API server's local TZ.
func bucketEnd(t time.Time, dur time.Duration) time.Time {
	if dur <= 0 {
		return t.UTC()
	}
	u := t.UTC()
	n := u.UnixNano()
	step := dur.Nanoseconds()
	// Right-closed: a row exactly on a boundary belongs to the bucket
	// that ends at the boundary, not the next one. This is what the
	// API summary's `time <= $to` lookup expects.
	idx := n / step
	if n%step != 0 {
		idx++
	}
	if idx == 0 {
		idx = 1
	}
	return time.Unix(0, idx*step).UTC()
}

// cleanRawValues applies the same UINT32 sentinel / non-finite filter
// the live aggregator's Submit uses, so backfill replays produce the
// same flow numbers a fresh poll cycle would. The filter mirrors
// aggregator.Submit verbatim; keeping the two paths in sync matters
// because divergence would silently shift recomputed totals away from
// the live aggregator's running totals.
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
// full set of five accumulators, so a half-populated bucket is dropped
// rather than fed in.
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

// FormatSeed renders a seed map as a stable, human-readable string.
// Used in API logs so an operator can correlate a backfill run with
// the live aggregator's prior cumulative state.
func FormatSeed(seed map[string]float64) string {
	if len(seed) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(seed))
	for k := range seed {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := "{"
	for i, k := range keys {
		if i > 0 {
			out += ", "
		}
		out += fmt.Sprintf("%s=%g", k, seed[k])
	}
	out += "}"
	return out
}
