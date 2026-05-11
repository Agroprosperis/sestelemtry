package api

import (
	"context"
	"fmt"
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
)

// maxEnergyFlowWindow caps how wide a window the on-the-fly
// energy-flow allocator is allowed to chew through inside a single
// /energy-summary request. We temporarily restrict it to day-sized
// windows (with slack for the longest local-time → UTC offset and
// week-boundary cases) because re-running the per-minute allocator
// across a full month or year synchronously takes several seconds
// and would block the API worker for every dashboard refresh. When
// we ship the daily-flow cache this guard can be raised or removed.
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
// directional flow totals computed on the fly from raw Modbus
// counters. Behaviour:
//
//   - If the caller didn't request any synthetic key, resp.Flows
//     stays nil.
//   - If the window exceeds maxEnergyFlowWindow, the compute is
//     skipped (resp.Flows stays nil) and an info log is emitted.
//   - On a compute failure resp.Flows still becomes a non-nil
//     pointer with all-zero values, so the client can distinguish
//     "not computed" (nil) from "computed but allocator dropped
//     every bucket" (zero struct). A warn log captures the cause.
//   - On success resp.Flows is the populated struct.
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
) {
	if resp == nil || !syntheticKeysRequested(effectiveKeys) {
		return
	}
	if to.Sub(from) > maxEnergyFlowWindow {
		// Wider-than-day windows skip the on-the-fly computation:
		// until we add a daily-rollup cache, re-running the
		// allocator on a month/year of raw rows would block the
		// request for seconds. The dashboard hides the period-flow
		// card for these presets, so the nil never reaches the UI;
		// direct API consumers see resp.Flows == nil and know flows
		// weren't computed.
		h.log.Info("api_energy_summary_flow_skipped_wide_window",
			"organization_id", orgID,
			"window", to.Sub(from).String(),
			"max_window", maxEnergyFlowWindow.String(),
		)
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
		return
	}
	resp.Flows = flows
	h.log.Info("api_energy_summary_flow_compute_ok",
		"organization_id", orgID,
		"duration_ms", time.Since(flowStart).Milliseconds(),
	)
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
	})
	return &EnergyFlowTotals{
		PVToESSKwh:   rec.Totals[energyflow.MetricPVToESSKwh],
		GridToESSKwh: rec.Totals[energyflow.MetricGridToESSKwh],
		ESSToLoadKwh: rec.Totals[energyflow.MetricESSToLoadKwh],
		ESSToGridKwh: rec.Totals[energyflow.MetricESSToGridKwh],
	}, nil
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
