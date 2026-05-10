package energyflow

import "math"

// IsInvalidUint32Scaled reports whether value is the SmartLogger's
// well-known UINT32 "all-ones" sentinel scaled by gain. Mirrors the
// helper in the spec:
//
//	const invalid = 4294967295 * gain
//	return |value - invalid| < gain * 10
//
// Used to filter daily-counter readings such as 42949672.95 (which is
// 0xFFFFFFFF * 0.01) from per-day registers that the SmartLogger
// emits while a value is still uninitialized. Matching the value
// inside `gain * 10` of the sentinel rather than against an exact
// equality tolerates the float64 round-trip of the gain multiplier.
func IsInvalidUint32Scaled(value, gain float64) bool {
	if !isFiniteFloat(gain) || gain <= 0 {
		return false
	}
	if !isFiniteFloat(value) {
		return false
	}
	invalid := 4294967295.0 * gain
	return math.Abs(value-invalid) < gain*10
}

// validateInterval checks dtSeconds against options. Returns a skip
// flag and a stable, human-readable reason when the interval should
// be discarded.
func validateInterval(dtSeconds float64, opts Options) (bool, string) {
	if !isFiniteFloat(dtSeconds) {
		return true, "dt non-finite"
	}
	if dtSeconds <= 0 {
		return true, "dt non-positive"
	}
	if opts.MaxGapSeconds > 0 && dtSeconds > float64(opts.MaxGapSeconds) {
		return true, "dt exceeds maxGapSeconds"
	}
	return false, ""
}

// isFiniteFloat is a small wrapper for !NaN && !Inf so the rest of
// the package reads naturally.
func isFiniteFloat(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}
