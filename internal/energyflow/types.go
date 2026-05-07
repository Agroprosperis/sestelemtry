// Package energyflow computes the four derived energy flows (ess→grid,
// ess→load, pv→ess, grid→ess) from per-second snapshots of two
// SmartLoggers (PV-side and ESS-side) using deltas of accumulator
// counters. The algorithm is a direct implementation of the project
// spec "ТЗ для агента: розрахунок потоків енергії УЗЕ".
//
// The package is pure: `Calculate` takes two snapshots plus running
// totals and returns an `IntervalDelta` plus warnings without touching
// any I/O. Persistence and goroutine wiring live in
// `cmd/collector/main.go` and `internal/storage`.
package energyflow

// EnergySample is one synchronized snapshot built from both SmartLoggers
// (PV @ 10.28.40.101, ESS @ 10.28.40.102) at roughly the same instant.
//
// Pointer-typed fields are optional accumulators / instantaneous
// readings: when missing, the calculator either skips the interval
// (`Calculate` returns Skipped=true) or falls back to instantaneous
// power according to the spec's priority rules.
//
// Field names mirror the TS types in the spec so reviewers can
// cross-reference 1:1.
type EnergySample struct {
	// Timestamp is the collector-side capture time. It is always
	// populated; device epoch fields (`PvDeviceEpochSeconds`,
	// `EssDeviceEpochSeconds`) only appear when both SmartLoggers
	// supplied a fresh `40000` reading on the same poll.
	Timestamp int64 // unix seconds (UTC)

	PvDeviceEpochSeconds        *int64
	EssDeviceEpochSeconds       *int64
	PvDeviceLocalEpochSeconds   *int64
	EssDeviceLocalEpochSeconds  *int64
	PvTimezoneOffsetSeconds     *int64
	EssTimezoneOffsetSeconds    *int64
	PvDstState                  *int64
	EssDstState                 *int64
	PvDstOffsetMinutes          *int64
	EssDstOffsetMinutes         *int64

	PvPowerKw   float64
	EssPowerKw  float64
	LoadPowerKw float64
	GridPowerKw *float64
	SocPercent  *float64

	TotalEssChargedKwh      *float64
	TotalEssDischargedKwh   *float64
	TotalGridSupplyToEssKwh *float64
	AccumulatedPvYieldKwh   *float64
	AccumulatedPurchasedKwh *float64
	AccumulatedSoldKwh      *float64
	AccumulatedLoadKwh      *float64
}

// EnergyFlowOptions tunes the calculator. All fields are honored
// directly; defaults from the spec are applied by `WithDefaults` so the
// caller can pass a zero-value `EnergyFlowOptions{}` and still get
// recommended behaviour.
type EnergyFlowOptions struct {
	EssDischargeSign         int
	PollIntervalSeconds      int
	AllocationWindowSeconds  int
	MaxGapSeconds            int
	MaxDeviceTimeSkewSeconds int
	BalanceToleranceKwh      float64
	ActivePvPowerAddress     int
}

// DefaultOptions returns the values from the spec's `DEFAULT_OPTIONS`
// table. Used by `WithDefaults` and reachable directly by tests / docs.
func DefaultOptions() EnergyFlowOptions {
	return EnergyFlowOptions{
		EssDischargeSign:         1,
		PollIntervalSeconds:      1,
		AllocationWindowSeconds:  60,
		MaxGapSeconds:            5,
		MaxDeviceTimeSkewSeconds: 2,
		BalanceToleranceKwh:      0.1,
		ActivePvPowerAddress:     440388,
	}
}

// WithDefaults returns a copy of opts with every zero-valued field
// replaced by its spec-recommended default. Idempotent.
func WithDefaults(opts EnergyFlowOptions) EnergyFlowOptions {
	d := DefaultOptions()
	if opts.EssDischargeSign == 0 {
		opts.EssDischargeSign = d.EssDischargeSign
	}
	if opts.PollIntervalSeconds == 0 {
		opts.PollIntervalSeconds = d.PollIntervalSeconds
	}
	if opts.AllocationWindowSeconds == 0 {
		opts.AllocationWindowSeconds = d.AllocationWindowSeconds
	}
	if opts.MaxGapSeconds == 0 {
		opts.MaxGapSeconds = d.MaxGapSeconds
	}
	if opts.MaxDeviceTimeSkewSeconds == 0 {
		opts.MaxDeviceTimeSkewSeconds = d.MaxDeviceTimeSkewSeconds
	}
	if opts.BalanceToleranceKwh == 0 {
		opts.BalanceToleranceKwh = d.BalanceToleranceKwh
	}
	if opts.ActivePvPowerAddress == 0 {
		opts.ActivePvPowerAddress = d.ActivePvPowerAddress
	}
	return opts
}

// IntervalDelta is what `Calculate` produces for one valid (prev,
// curr) interval. All deltas are in kWh and >= 0; the calculator
// applies clamps and the spec's allocation rules before populating
// these fields. Skipped=true means the interval was rejected (e.g.
// negative dt, time skew too big, invalid UINT32 sentinel) and the
// other fields are zeroed.
type IntervalDelta struct {
	Skipped bool

	EssToGridKwh float64
	EssToLoadKwh float64
	PvToEssKwh   float64
	GridToEssKwh float64

	DeltaPvYieldKwh        float64
	DeltaLoadKwh           float64
	DeltaGridToEssKwh      float64
	DeltaEssChargedKwh     float64
	DeltaEssDischargedKwh  float64

	DtSeconds float64
	Warnings  []string
}

// RunningTotals tracks the cumulative kWh per flow since process
// start. The collector's per-org goroutine owns one of these and adds
// the latest IntervalDelta into it before persisting.
type RunningTotals struct {
	EssToGridKwh float64
	EssToLoadKwh float64
	PvToEssKwh   float64
	GridToEssKwh float64
}

// Add folds an IntervalDelta into the totals. Skipped deltas are a
// no-op; the warning list of the delta is the caller's to surface.
func (r *RunningTotals) Add(d IntervalDelta) {
	if d.Skipped {
		return
	}
	r.EssToGridKwh += d.EssToGridKwh
	r.EssToLoadKwh += d.EssToLoadKwh
	r.PvToEssKwh += d.PvToEssKwh
	r.GridToEssKwh += d.GridToEssKwh
}
