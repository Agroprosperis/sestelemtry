package api

import (
	"context"
	"fmt"
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
)

// maxEnergyFlowWindow caps how wide a window the on-the-fly
// energy-flow allocator is allowed to chew through inside a single
// /energy-summary request. Day-sized windows (with slack for the
// longest local-time → UTC offset and week-boundary cases) run the
// allocator live; anything wider is served from the per-day totals
// the economics daemon has already persisted, because re-running the
// per-minute allocator across a month or year synchronously takes
// minutes and would block the API worker for every dashboard refresh.
const maxEnergyFlowWindow = 36 * time.Hour

// syntheticKeysRequested reports whether the caller's metric_keys
// list contains at least one of the four synthetic energy-flow
// counters. Used by the EnergySummary handler to decide whether to
// run the on-the-fly allocation pipeline.
func syntheticKeysRequested(keys []string) bool {
	want := make(map[string]struct{}, len(EnergyFlowSyntheticMetrics))
	for _, k := range EnergyFlowSyntheticMetrics {
		want[k] = struct{}{}
	}
	for _, k := range keys {
		if _, ok := want[k]; ok {
			return true
		}
	}
	return false
}

// maybeAttachEnergyFlow conditionally fills resp.Flows with the four
// directional flow totals, and resp.FlowsMeta with the pipeline that
// produced them. Behaviour:
//
//   - If the caller didn't request any synthetic key, resp.Flows
//     stays nil.
//   - Windows up to maxEnergyFlowWindow run the allocator live over
//     raw Modbus counters.
//   - Wider windows are summed from the per-day totals the economics
//     daemon persisted; resp.Flows stays nil when it has no day in
//     range at all.
//   - When the allocator itself fails, resp.Flows still becomes a
//     non-nil pointer with all-zero values, so the client can
//     distinguish "not computed" (nil) from "computed but the
//     allocator dropped every bucket" (zero struct). A warn log
//     captures the cause.
//   - On success resp.Flows is the populated struct.
//
// `loc` is the timezone whose civil days the cached rollup is keyed
// by; it only matters for the wide-window path.
//
// The caller writes the response unchanged; this helper never sets
// HTTP status codes so the wider summary path stays useful even if
// the allocator can't run.
func (h *Handlers) maybeAttachEnergyFlow(
	ctx context.Context,
	resp *EnergySummaryResponse,
	orgID string,
	from, to time.Time,
	effectiveKeys []string,
	loc *time.Location,
) {
	if resp == nil || !syntheticKeysRequested(effectiveKeys) {
		return
	}
	if to.Sub(from) > maxEnergyFlowWindow {
		h.attachEnergyFlowFromDailyCache(ctx, resp, orgID, from, to, loc)
		return
	}
	flowStart := time.Now()
	flows, ferr := h.computeEnergyFlowTotals(ctx, orgID, from, to)
	if ferr != nil {
		// Degrade gracefully: the rest of the summary (raw
		// counters) is still useful, and the four flow values
		// fall back to zero. We emit a non-nil pointer so the
		// client can tell apart "we tried and got zero" from
		// "we didn't run the compute".
		h.log.Warn("api_energy_summary_flow_compute",
			"organization_id", orgID, "err", ferr,
			"duration_ms", time.Since(flowStart).Milliseconds(),
		)
		resp.Flows = &EnergyFlowTotals{}
		resp.FlowsMeta = &EnergyFlowMeta{Source: EnergyFlowSourceAllocator}
		return
	}
	resp.Flows = flows
	resp.FlowsMeta = &EnergyFlowMeta{Source: EnergyFlowSourceAllocator}
	h.log.Info("api_energy_summary_flow_compute_ok",
		"organization_id", orgID,
		"duration_ms", time.Since(flowStart).Milliseconds(),
	)
}

// attachEnergyFlowFromDailyCache serves month/year windows by summing
// the per-day flow totals in economics_daily. Those rows were produced
// by the same allocator, one day at a time, by the economics-recompute
// daemon — reconciled against the metered charge/discharge counters,
// which puts them within ~0.1 % of a live run over the same day.
//
// The current day is included from the cache too, so its contribution
// lags by up to economics.today_interval. Against a month or year
// total that drift is a rounding error, and it keeps the request to a
// single indexed aggregate that the dashboard can poll.
//
// resp.Flows stays nil when the cache holds no day in range: an
// all-zero struct would read as "the period moved no energy", which is
// a very different claim from "nobody has computed this period yet".
func (h *Handlers) attachEnergyFlowFromDailyCache(
	ctx context.Context,
	resp *EnergySummaryResponse,
	orgID string,
	from, to time.Time,
	loc *time.Location,
) {
	fromDay, toDay, expected := civilDaySpan(from, to, loc)
	if expected <= 0 {
		return
	}
	totals, covered, err := h.store.EnergyFlowDailyTotals(ctx, orgID, fromDay, toDay)
	if err != nil {
		// Unlike a failed allocator run, a failed cache read tells us
		// nothing about the period, so Flows stays nil ("not computed")
		// rather than becoming the zero struct ("computed, found
		// nothing"). A deployment without the economics schema lands
		// here and correctly shows placeholders.
		h.log.Warn("api_energy_summary_flow_daily_cache",
			"organization_id", orgID, "err", err,
			"from_day", fromDay.Format("2006-01-02"),
			"to_day", toDay.Format("2006-01-02"),
		)
		return
	}
	if covered == 0 {
		h.log.Info("api_energy_summary_flow_daily_cache_empty",
			"organization_id", orgID,
			"from_day", fromDay.Format("2006-01-02"),
			"to_day", toDay.Format("2006-01-02"),
			"days_expected", expected,
		)
		return
	}
	resp.Flows = &totals
	resp.FlowsMeta = &EnergyFlowMeta{
		Source:       EnergyFlowSourceDailyCache,
		DaysCovered:  covered,
		DaysExpected: expected,
	}
	h.log.Info("api_energy_summary_flow_daily_cache_ok",
		"organization_id", orgID,
		"days_covered", covered,
		"days_expected", expected,
	)
}

// civilDaySpan maps the half-open instant window [from, to) onto the
// inclusive span of civil days it touches in loc, plus how many days
// that is. `to` being exclusive is the point: a month window ending at
// the first midnight of the next month must not pull that next day's
// row into the sum.
func civilDaySpan(from, to time.Time, loc *time.Location) (fromDay, toDay time.Time, days int) {
	if loc == nil {
		loc = time.UTC
	}
	first := startOfCivilDay(from, loc)
	// The last instant the window actually covers. Clamped to `from`
	// so a degenerate range still names one day instead of walking
	// backwards.
	lastInstant := to.Add(-time.Nanosecond)
	if lastInstant.Before(from) {
		lastInstant = from
	}
	last := startOfCivilDay(lastInstant, loc)
	if last.Before(first) {
		return time.Time{}, time.Time{}, 0
	}
	// Count through the calendar rather than dividing the elapsed
	// duration: a DST shift inside the window makes it 23 or 25 hours
	// short of a whole number of days and rounds the count off by one.
	days = int(civilDayIndex(last)-civilDayIndex(first)) + 1
	return first, last, days
}

func startOfCivilDay(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	y, m, d := local.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// civilDayIndex numbers calendar days so two of them can be
// subtracted without the timezone offsets cancelling incorrectly.
func civilDayIndex(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / int64(24*time.Hour/time.Second)
}

// computeEnergyFlowTotals runs the allocation rule against the raw
// Modbus counters stored in telemetry_samples for [from, to] and
// returns the four period flow totals (kWh).
//
// There is no shared cumulative state to corrupt: rewriting an old
// period is just "click refresh", and missing periods (e.g. before
// the energy-flow feature shipped) are computed identically to fresh
// ones with no manual ops work.
//
// CPU cost is modest: a day query is ~1.4k 60s buckets, a month
// ~43k. Each bucket is a handful of float multiplications inside
// energyflow.Allocate, so day-preset runs are sub-second. The
// maybeAttachEnergyFlow guard prevents the API from being asked to
// crunch a month or year synchronously.
func (h *Handlers) computeEnergyFlowTotals(
	ctx context.Context,
	orgID string,
	from, to time.Time,
) (*EnergyFlowTotals, error) {
	rows, err := h.store.EnergyFlowSources(ctx, orgID, from, to, 0)
	if err != nil {
		return nil, fmt.Errorf("energy_flow sources: %w", err)
	}
	if len(rows) == 0 {
		return &EnergyFlowTotals{}, nil
	}
	// Org config is optional: legacy single-device deployments do
	// not register a device→role map, and buildRawSamples maps any
	// row with an empty device_host label to RoleSingle in that
	// case. Multi-SmartLogger orgs need the config to attribute
	// rows to the correct role.
	cfg := h.energyFlowOrgs[orgID]
	rawSamples := buildRawSamples(rows, cfg)
	rec := energyflow.Recompute(rawSamples, energyflow.Options{
		EssDischargeSign:        cfg.EssDischargeSign,
		AllocationWindowSeconds: 60,
		MaxGapSeconds:           0, // disabled — historical windows can span collector outages
		MaxEssPowerKw:           essPowerCeiling(cfg),
	})
	return &EnergyFlowTotals{
		PVToESSKwh:   rec.Totals[energyflow.MetricPVToESSKwh],
		GridToESSKwh: rec.Totals[energyflow.MetricGridToESSKwh],
		ESSToLoadKwh: rec.Totals[energyflow.MetricESSToLoadKwh],
		ESSToGridKwh: rec.Totals[energyflow.MetricESSToGridKwh],
	}, nil
}

// essPowerCeiling returns the ESS charge/discharge power ceiling (kW)
// the allocator's counter-step guard uses for this org. An explicit
// per-org ess_max_power_kw wins; otherwise the fleet-wide default
// applies so the guard is always active on the dashboard / economics
// paths without any per-org configuration.
func essPowerCeiling(cfg EnergyFlowOrg) float64 {
	if cfg.EssMaxPowerKw > 0 {
		return cfg.EssMaxPowerKw
	}
	return energyflow.DefaultMaxEssPowerKw
}

// buildRawSamples groups telemetry rows into energyflow.RawSample by
// (time, device_host). The device_host label is mapped to a Role via
// the org config; unknown hosts default to RoleSingle which is the
// safest classification for legacy single-device deployments (no
// device_host label) and avoids dropping data when an operator
// renames a SmartLogger between collection and on-the-fly compute.
func buildRawSamples(rows []EnergyFlowRawRow, cfg EnergyFlowOrg) []energyflow.RawSample {
	roleByHost := make(map[string]energyflow.Role, len(cfg.Devices))
	for _, d := range cfg.Devices {
		roleByHost[d.Host] = energyflow.Role(d.Role)
	}
	type key struct {
		t    int64
		host string
	}
	grouped := make(map[key]map[string]float64, len(rows)/3+1)
	order := make([]key, 0, len(rows)/3+1)
	for _, r := range rows {
		k := key{t: r.Time.UnixNano(), host: r.DeviceHost}
		bucket, ok := grouped[k]
		if !ok {
			bucket = make(map[string]float64, 5)
			grouped[k] = bucket
			order = append(order, k)
		}
		bucket[r.MetricKey] = r.Value
	}
	out := make([]energyflow.RawSample, 0, len(order))
	for _, k := range order {
		role, ok := roleByHost[k.host]
		if !ok {
			if len(cfg.Devices) == 0 {
				role = energyflow.RoleSingle
			} else if k.host == "" {
				role = energyflow.RoleSingle
			} else {
				continue
			}
		}
		if role == energyflow.RoleNone {
			continue
		}
		out = append(out, energyflow.RawSample{
			Time:   time.Unix(0, k.t).UTC(),
			Role:   role,
			Values: grouped[k],
		})
	}
	return out
}
