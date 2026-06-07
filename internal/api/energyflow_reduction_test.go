package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
)

// ceilMinuteKey mirrors energyflow.bucketEnd's bucket index for a
// 60-second window: ceil(unixNano / 60e9). This is the exact
// right-closed convention the Go allocator uses, and the SQL
// reduction in Store.EnergyFlowSources reproduces it with
// ceil(extract(epoch)/60). Used by the test-only reducer below to
// model what the database now returns.
func ceilMinuteKey(t time.Time) int64 {
	const step = int64(60) * int64(time.Second)
	n := t.UTC().UnixNano()
	idx := n / step
	if n%step != 0 {
		idx++
	}
	return idx
}

// isSentinelOrNonFinite mirrors energyflow.cleanRawValues' per-value
// drop rule (NaN/Inf + the three SmartLogger UINT32 sentinels) so the
// test reducer keeps the same representative the Go cleaner would.
func isSentinelOrNonFinite(v float64) bool {
	if v != v { // NaN
		return true
	}
	if energyflow.IsInvalidUint32Scaled(v, 0.01) ||
		energyflow.IsInvalidUint32Scaled(v, 0.001) ||
		energyflow.IsInvalidUint32Scaled(v, 0.1) {
		return true
	}
	return false
}

// reduceToCeilMinuteLast models the new SQL: one (max(time),
// last(value, time)) per (metric_key, device_host, ceil-minute),
// dropping sentinel / non-finite values before picking the
// representative. Output is sorted (time ASC, metric_key ASC) to
// match the query's ORDER BY and the allocator's monotonic-input
// requirement.
func reduceToCeilMinuteLast(rows []EnergyFlowRawRow) []EnergyFlowRawRow {
	type gkey struct {
		metric string
		host   string
		minute int64
	}
	type rep struct {
		t time.Time
		v float64
	}
	best := make(map[gkey]rep)
	for _, r := range rows {
		if isSentinelOrNonFinite(r.Value) {
			continue
		}
		k := gkey{metric: r.MetricKey, host: r.DeviceHost, minute: ceilMinuteKey(r.Time)}
		cur, ok := best[k]
		if !ok || !r.Time.Before(cur.t) {
			best[k] = rep{t: r.Time, v: r.Value}
		}
	}
	out := make([]EnergyFlowRawRow, 0, len(best))
	for k, v := range best {
		out = append(out, EnergyFlowRawRow{
			Time:       v.t,
			MetricKey:  k.metric,
			Value:      v.v,
			DeviceHost: k.host,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Time.Equal(out[j].Time) {
			return out[i].Time.Before(out[j].Time)
		}
		return out[i].MetricKey < out[j].MetricKey
	})
	return out
}

// hourlyFlows runs the real handler path (buildRawSamples +
// IterateIntervals, partitioned into 24 hours) over the given rows
// and returns the response. Reusing the handler keeps the test
// honest: it exercises the exact code that powers the dashboard and
// the economics recompute.
func hourlyFlows(t *testing.T, rows []EnergyFlowRawRow, date, tz string) EnergyFlowHourlyResponse {
	t.Helper()
	store := &mockStore{flowSources: rows}
	h := NewHandlers(store, "*")
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/energy-flow-hourly?organization_id=demo&date="+date+"&tz="+tz, nil)
	rec := httptest.NewRecorder()
	h.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200 got %d, body=%s", rec.Code, rec.Body.String())
	}
	var body EnergyFlowHourlyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return body
}

// secondlyDayRows generates one snapshot PER SECOND for `hours` hours
// starting at dayStart, mimicking the live collector's 1 Hz cadence.
// Counters tick smoothly; to stress the reducer it injects a UINT32
// sentinel on the PV counter once a minute (which the allocator must
// ignore, falling back to the previous good reading) and places a
// sample exactly on a minute boundary so the ceil bucketing is
// exercised. Empty device_host → RoleSingle under an empty org cfg.
func secondlyDayRows(dayStart time.Time, hours int) []EnergyFlowRawRow {
	const sentinelPV = 42949672.95 // 0xFFFFFFFF * 0.01
	pv := 1000.0
	purchased := 500.0
	sold := 200.0
	charged := 300.0
	discharged := 250.0
	total := hours * 3600
	rows := make([]EnergyFlowRawRow, 0, total*5)
	for s := 0; s <= total; s++ {
		ts := dayStart.Add(time.Duration(s) * time.Second)
		// Smooth, strictly non-decreasing accumulators with an
		// hour-varying rate so per-hour attribution is non-trivial.
		hour := float64(s) / 3600.0
		pvRate := 0.0
		if hour > 6 && hour < 20 {
			pvRate = 0.0016
		}
		pv += pvRate
		purchased += 0.0009
		sold += 0.0011
		charged += 0.0013
		discharged += 0.0007

		pvVal := pv
		// Once a minute, the PV counter reports the sentinel instead
		// of the real value. cleanRawValues drops it, so the minute's
		// representative PV must come from the prior second.
		if s%60 == 30 {
			pvVal = sentinelPV
		}
		rows = append(rows,
			EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcAccumulatedPVYieldKwh, Value: pvVal},
			EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcAccumulatedPurchasedKwh, Value: purchased},
			EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcAccumulatedSoldKwh, Value: sold},
			EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcTotalEssChargedKwh, Value: charged},
			EnergyFlowRawRow{Time: ts, MetricKey: energyflow.SrcTotalEssDischargedKwh, Value: discharged},
		)
	}
	return rows
}

// TestEnergyFlowSources_PerMinuteReductionEquivalent is the proof the
// user asked for: collapsing the per-second telemetry into one
// last()-per-minute row (as Store.EnergyFlowSources now does in SQL)
// produces byte-identical hourly flows to feeding the raw per-second
// stream through the allocator. We model the SQL reduction with
// reduceToCeilMinuteLast and compare both inputs through the real
// handler path, including a sentinel-once-a-minute torture and a
// minute-boundary sample.
func TestEnergyFlowSources_PerMinuteReductionEquivalent(t *testing.T) {
	dayStart := time.Date(2026, 5, 31, 0, 0, 0, 0, time.UTC)
	raw := secondlyDayRows(dayStart, 24)
	reduced := reduceToCeilMinuteLast(raw)

	// Sanity: the reduction must shrink the row count by roughly 60×
	// (one row per metric per minute vs per second), otherwise the
	// test isn't exercising what it claims.
	if len(reduced) >= len(raw)/10 {
		t.Fatalf("reduction too weak: raw=%d reduced=%d", len(raw), len(reduced))
	}

	fromRaw := hourlyFlows(t, raw, "2026-05-31", "UTC")
	fromReduced := hourlyFlows(t, reduced, "2026-05-31", "UTC")

	if len(fromRaw.Hours) != len(fromReduced.Hours) {
		t.Fatalf("hour count mismatch: raw=%d reduced=%d", len(fromRaw.Hours), len(fromReduced.Hours))
	}
	const tol = 1e-9
	for i := range fromRaw.Hours {
		a, b := fromRaw.Hours[i], fromReduced.Hours[i]
		checks := []struct {
			name   string
			ra, rb float64
		}{
			{"pv_to_ess", a.PVToESSKwh, b.PVToESSKwh},
			{"grid_to_ess", a.GridToESSKwh, b.GridToESSKwh},
			{"ess_to_load", a.ESSToLoadKwh, b.ESSToLoadKwh},
			{"ess_to_grid", a.ESSToGridKwh, b.ESSToGridKwh},
			{"ess_charged", a.EssChargedKwh, b.EssChargedKwh},
			{"ess_discharged", a.EssDischargedKwh, b.EssDischargedKwh},
		}
		for _, c := range checks {
			if !floatNearAPI(c.ra, c.rb, tol) {
				t.Fatalf("hour %d %s mismatch: raw=%.9f reduced=%.9f", i, c.name, c.ra, c.rb)
			}
		}
	}
}
