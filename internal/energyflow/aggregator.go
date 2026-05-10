package energyflow

import (
	"context"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"
)

// EmittedSample is one cumulative-counter sample produced by the
// aggregator and handed to the collector for persistence. The
// aggregator stays storage-agnostic on purpose so unit tests can
// drive Flush without spinning up a real database.
type EmittedSample struct {
	Time      time.Time
	MetricKey string
	Value     float64
}

// EmitFunc persists the four flush samples produced by one window
// flush. Returning an error lets the aggregator log and surface a
// diagnostic counter without stopping the periodic flush goroutine.
type EmitFunc func(ctx context.Context, samples []EmittedSample) error

// Diagnostics is the aggregate quality summary kept by the
// aggregator over its lifetime. The `Warnings` slice is bounded
// (recent-tail) so a long-running collector does not balloon memory.
type Diagnostics struct {
	WindowsFlushed int
	WindowsSkipped int
	InvalidSamples int
	Warnings       []string
}

// Aggregator is the per-organization stateful merger. The collector
// creates one per org, calls Submit from each device's poll loop,
// and runs a Flush goroutine on the spec's allocation_window cadence
// (default 60 s). Flush computes the delta against the previously
// flushed snapshot, accumulates the four directional flow values
// into running cumulative totals, and emits one sample per metric
// per flush so the API's last(end) - last(seed) summary path keeps
// working without a schema change.
type Aggregator struct {
	orgID     string
	opts      Options
	allocOpts Options
	log       *slog.Logger
	emit      EmitFunc
	now       func() time.Time

	mu              sync.Mutex
	latest          map[Role]*roleSnapshot
	prevAlloc       *Sample
	cumulative      map[string]float64
	maxWarnings     int
	maxKeptWarnings int
	diag            Diagnostics
}

// roleSnapshot is the most recent reading from one logical role.
// The map key is the source metric_key (active_pv_power_kw,
// accumulated_pv_energy_yield_kwh, …); values are the already-decoded
// engineering-unit numbers the rest of the collector writes to
// telemetry_samples.
type roleSnapshot struct {
	timestamp time.Time
	values    map[string]float64
}

// New builds an aggregator with the spec defaults and the given emit
// callback. opts may override any default; emit must be non-nil. The
// aggregator does not start its goroutine until Run is called.
func New(orgID string, opts Options, emit EmitFunc, log *slog.Logger) *Aggregator {
	if log == nil {
		log = slog.Default()
	}
	o := opts.WithDefaults()
	// Allocate's MaxGapSeconds gates dt validation. Inside the
	// aggregator the dt between two flushed snapshots is exactly
	// the allocation window, so we relax the Allocate-level cap to
	// 2× the window. The aggregator runs its own freshness check
	// below using `MaxGapSeconds` as documented.
	allocOpts := o
	allocOpts.MaxGapSeconds = 2 * o.AllocationWindowSeconds
	return &Aggregator{
		orgID:           orgID,
		opts:            o,
		allocOpts:       allocOpts,
		log:             log.With("organization_id", orgID, "component", "energyflow"),
		emit:            emit,
		now:             time.Now,
		latest:          make(map[Role]*roleSnapshot),
		cumulative:      make(map[string]float64, len(SyntheticMetricKeys)),
		maxKeptWarnings: 64,
	}
}

// Reseed installs the running cumulative totals from a previous
// process. The collector queries the latest value of each
// SyntheticMetricKeys entry from telemetry_samples on startup and
// passes the result here so a process restart does not reset the
// dashboard's lifetime totals to zero.
//
// Unknown keys are ignored. NaN/Inf values are dropped with a
// warning so a poisoned DB read can not corrupt the running totals.
func (a *Aggregator) Reseed(seeds map[string]float64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, k := range SyntheticMetricKeys {
		v, ok := seeds[k]
		if !ok {
			continue
		}
		if !isFiniteFloat(v) || v < 0 {
			a.diag.InvalidSamples++
			a.appendWarning("reseed dropped non-finite/negative value for " + k)
			continue
		}
		a.cumulative[k] = v
	}
}

// Submit hands a freshly polled snapshot from one device to the
// aggregator. role identifies which logical SmartLogger produced it
// (RolePV, RoleESS or RoleSingle); ts is the canonical server-side
// timestamp the collector wrote alongside the samples; values is the
// decoded engineering-unit map of metric_key → value for whatever
// metric_keys the device polled.
//
// Sentinel readings such as 0xFFFFFFFF * gain (e.g. 42949672.95) are
// filtered before they enter the aggregator state so a brief
// SmartLogger UINT32 overflow can not poison the next window's delta.
func (a *Aggregator) Submit(role Role, ts time.Time, values map[string]float64) {
	if role == RoleNone || len(values) == 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	clean := make(map[string]float64, len(values))
	for k, v := range values {
		if !isFiniteFloat(v) {
			a.diag.InvalidSamples++
			a.appendWarning("dropped non-finite value for " + k)
			continue
		}
		// Daily UINT32 sentinel: 0xFFFFFFFF * 0.01 ≈ 42949672.95
		// for the gain-0.01 catalog entries; the rare gain-0.001
		// or gain-0.1 sentinels match too. Filtering against both
		// scales catches the spec's documented daily-counter
		// overflow without needing a per-key gain lookup.
		if IsInvalidUint32Scaled(v, 0.01) ||
			IsInvalidUint32Scaled(v, 0.001) ||
			IsInvalidUint32Scaled(v, 0.1) {
			a.diag.InvalidSamples++
			a.appendWarning("dropped UINT32 sentinel for " + k)
			continue
		}
		clean[k] = v
	}
	if len(clean) == 0 {
		return
	}
	a.latest[role] = &roleSnapshot{timestamp: ts, values: clean}
}

// Flush builds a merged Sample from the latest readings of each
// role, runs Allocate against the previously flushed snapshot, and
// emits one cumulative-counter sample per synthetic metric. It is
// safe to call concurrently with Submit and is idempotent if no new
// data has arrived since the last call (the freshness check rejects
// the window).
func (a *Aggregator) Flush(ctx context.Context) error {
	curr, ok := a.snapshot()
	if !ok {
		return nil
	}
	a.mu.Lock()
	prev := a.prevAlloc
	a.prevAlloc = &curr
	a.mu.Unlock()

	if prev == nil {
		// First flush — nothing to subtract against. Cumulative
		// totals stay at their reseed values; the next flush emits
		// the first real delta.
		return nil
	}

	res := Allocate(*prev, curr, a.allocOpts)
	a.mu.Lock()
	for _, w := range res.Warnings {
		a.appendWarning(w)
	}
	if res.Skipped {
		a.diag.WindowsSkipped++
		a.mu.Unlock()
		return nil
	}
	a.diag.WindowsFlushed++
	a.cumulative[MetricPVToESSKwh] += res.PVToESSKwh
	a.cumulative[MetricGridToESSKwh] += res.GridToESSKwh
	a.cumulative[MetricESSToLoadKwh] += res.ESSToLoadKwh
	a.cumulative[MetricESSToGridKwh] += res.ESSToGridKwh
	out := make([]EmittedSample, 0, len(SyntheticMetricKeys))
	for _, k := range SyntheticMetricKeys {
		out = append(out, EmittedSample{
			Time:      curr.Timestamp,
			MetricKey: k,
			Value:     a.cumulative[k],
		})
	}
	a.mu.Unlock()

	if a.emit == nil {
		return nil
	}
	return a.emit(ctx, out)
}

// snapshot composes the current Sample from latestByRole. Returns
// false when the merge is impossible (no usable role, or one side
// of a dual deployment is missing/stale beyond MaxGapSeconds).
func (a *Aggregator) snapshot() (Sample, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()

	pickPV, pickESS, ok := a.pickRoles(now)
	if !ok {
		a.diag.WindowsSkipped++
		return Sample{}, false
	}

	// Diagnostic only: skew between the two SmartLoggers' server
	// receive timestamps. Per spec we never reject for skew until
	// NTP synchronization is verified — a stale clock on one box
	// is the dominant failure mode in the field.
	if pickPV != pickESS && a.opts.WarnDeviceTimeSkewSeconds > 0 {
		skew := math.Abs(pickPV.timestamp.Sub(pickESS.timestamp).Seconds())
		if skew > float64(a.opts.WarnDeviceTimeSkewSeconds) {
			a.appendWarning("device clock skew exceeds warn threshold")
		}
	}

	merged := Sample{Timestamp: laterOf(pickPV.timestamp, pickESS.timestamp)}
	mergeAccumulators(&merged, pickPV.values)
	mergeAccumulators(&merged, pickESS.values)
	mergePowers(&merged, pickPV.values)
	mergePowers(&merged, pickESS.values)
	return merged, true
}

// pickRoles returns the (pv, ess) sources to merge, preferring
// RoleSingle when present. ok is false when either side is missing
// or stale.
func (a *Aggregator) pickRoles(now time.Time) (*roleSnapshot, *roleSnapshot, bool) {
	if s, ok := a.latest[RoleSingle]; ok {
		if a.isStale(now, s.timestamp) {
			return nil, nil, false
		}
		return s, s, true
	}
	pv, pvOK := a.latest[RolePV]
	ess, essOK := a.latest[RoleESS]
	if !pvOK || !essOK {
		return nil, nil, false
	}
	if a.isStale(now, pv.timestamp) || a.isStale(now, ess.timestamp) {
		return nil, nil, false
	}
	return pv, ess, true
}

func (a *Aggregator) isStale(now, ts time.Time) bool {
	if a.opts.MaxGapSeconds <= 0 {
		return false
	}
	return now.Sub(ts) > time.Duration(a.opts.MaxGapSeconds)*time.Second
}

// Run drives Flush on the allocation_window cadence. It returns
// when ctx is canceled, performing one final Flush so a graceful
// shutdown does not lose the partial last window.
func (a *Aggregator) Run(ctx context.Context) {
	interval := time.Duration(a.opts.AllocationWindowSeconds) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	a.log.Info("energyflow_run_start", "allocation_window_s", a.opts.AllocationWindowSeconds)
	for {
		select {
		case <-ctx.Done():
			// Final flush in a fresh context so a parent cancel
			// doesn't block the storage write needed to persist
			// the last window's cumulative totals.
			finalCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := a.Flush(finalCtx); err != nil {
				a.log.Error("energyflow_final_flush", "err", err)
			}
			cancel()
			a.log.Info("energyflow_run_stop")
			return
		case <-ticker.C:
			if err := a.Flush(ctx); err != nil {
				a.log.Error("energyflow_flush", "err", err)
			}
		}
	}
}

// SnapshotDiagnostics returns a copy of the current diagnostics
// counters. Intended for log-line emission and tests.
func (a *Aggregator) SnapshotDiagnostics() Diagnostics {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := Diagnostics{
		WindowsFlushed: a.diag.WindowsFlushed,
		WindowsSkipped: a.diag.WindowsSkipped,
		InvalidSamples: a.diag.InvalidSamples,
	}
	if len(a.diag.Warnings) > 0 {
		out.Warnings = append([]string(nil), a.diag.Warnings...)
	}
	return out
}

// CumulativeSnapshot returns a copy of the running totals keyed by
// SyntheticMetricKeys. Intended for tests and ad-hoc inspection.
func (a *Aggregator) CumulativeSnapshot() map[string]float64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]float64, len(a.cumulative))
	for k, v := range a.cumulative {
		out[k] = v
	}
	return out
}

func (a *Aggregator) appendWarning(s string) {
	if a.maxKeptWarnings <= 0 {
		return
	}
	a.diag.Warnings = append(a.diag.Warnings, s)
	if len(a.diag.Warnings) > a.maxKeptWarnings {
		a.diag.Warnings = a.diag.Warnings[len(a.diag.Warnings)-a.maxKeptWarnings:]
	}
}

func mergeAccumulators(dst *Sample, src map[string]float64) {
	if v, ok := src[SrcAccumulatedPVYieldKwh]; ok {
		dst.AccumulatedPVYieldKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcAccumulatedPurchasedKwh]; ok {
		dst.AccumulatedPurchasedKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcAccumulatedSoldKwh]; ok {
		dst.AccumulatedSoldKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcAccumulatedPowerConsumptionKwh]; ok {
		dst.AccumulatedPowerConsumptionKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcTotalEssChargedKwh]; ok {
		dst.TotalESSChargedKwh = floatPtrCopy(v)
	}
	if v, ok := src[SrcTotalEssDischargedKwh]; ok {
		dst.TotalESSDischargedKwh = floatPtrCopy(v)
	}
}

func mergePowers(dst *Sample, src map[string]float64) {
	if v, ok := src[SrcActivePVPowerKw]; ok {
		dst.PVPowerKw = floatPtrCopy(v)
	}
	if v, ok := src[SrcActiveESSPowerKw]; ok {
		dst.ESSPowerKw = floatPtrCopy(v)
	}
	if v, ok := src[SrcLoadPowerKw]; ok {
		dst.LoadPowerKw = floatPtrCopy(v)
	}
	if v, ok := src[SrcGridConnectedActivePowerKw]; ok {
		dst.GridPowerKw = floatPtrCopy(v)
	}
	if v, ok := src[SrcSocPercent]; ok {
		dst.SOCPercent = floatPtrCopy(v)
	}
}

func floatPtrCopy(v float64) *float64 {
	out := v
	return &out
}

func laterOf(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// stableKeys returns sorted keys of m. Internal helper for tests.
func stableKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
