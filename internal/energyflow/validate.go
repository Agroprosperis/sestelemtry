package energyflow

import "math"

// IsInvalidInt32Scaled reports whether value is the SmartLogger's
// well-known INT32 "max" sentinel scaled by gain:
//
//	const invalid = 2147483647 * gain   // 0x7FFFFFFF
//	return |value - invalid| < gain * 10
//
// The grid-connected active power register (40505, INT32, gain
// 0.001) emits this when the POC meter is unreachable — typically
// islanding or a Modbus fault. Left untreated it surfaces as
// ~2_147_483.647 kW on the live energy-flow diagram.
func IsInvalidInt32Scaled(value, gain float64) bool {
	if !isFiniteFloat(gain) || gain <= 0 {
		return false
	}
	if !isFiniteFloat(value) {
		return false
	}
	invalidPos := 2147483647.0 * gain
	invalidNeg := -2147483648.0 * gain
	tol := gain * 10
	return math.Abs(value-invalidPos) < tol || math.Abs(value-invalidNeg) < tol
}

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
