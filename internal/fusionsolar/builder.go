package fusionsolar

import (
	"sort"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

// OrgHosts maps an organization's metric_keys to the live device_host
// the Modbus collector stamps for each one, so archive rows classify in
// the energy-flow allocator exactly as live rows do.
//
// On dual-SmartLogger sites (e.g. Zhmerynskyi) PV/grid/load counters and
// ESS charge/discharge counters live on *different* hosts; the allocator
// derives RolePV / RoleESS from device_host, so stamping every archive
// row with one host collapses both onto a single role and the allocator
// drops every bucket (no RolePV+RoleESS pair) — zeroing the directional
// flows for archived days. ByMetricKey carries the per-metric host;
// Default covers any metric not scoped to a specific device (the common
// single-logger case, where every metric shares one host).
type OrgHosts struct {
	Default     string
	ByMetricKey map[string]string
}

// hostFor returns the device_host to stamp on `metric`: the per-metric
// host when the org's config scopes the metric to a specific device,
// otherwise the org default.
func (h OrgHosts) hostFor(metric string) string {
	if h.ByMetricKey != nil {
		if v, ok := h.ByMetricKey[metric]; ok && v != "" {
			return v
		}
	}
	return h.Default
}

// sampleAccumulator folds FusionSolar device history into the
// deduplicated set of telemetry_samples the importer writes. It is the
// pure transform half of Import (no network, no DB) so the cumulative
// field mapping and SOC averaging can be unit-tested in isolation.
//
// Values are keyed by (metric_key, unix-milli) so a sample appearing at
// a window boundary in two successive 24h fetches collapses to one row.
type sampleAccumulator struct {
	orgID string
	hosts OrgHosts
	topo  PlantTopology

	values   map[cellKey]float64
	socSum   map[int64]float64
	socCount map[int64]int
}

type cellKey struct {
	metric string
	ms     int64
}

func newSampleAccumulator(orgID string, hosts OrgHosts, topo PlantTopology) *sampleAccumulator {
	return &sampleAccumulator{
		orgID:    orgID,
		hosts:    hosts,
		topo:     topo,
		values:   map[cellKey]float64{},
		socSum:   map[int64]float64{},
		socCount: map[int64]int{},
	}
}

// addLogger maps the primary SmartLogger's cumulative PV / load / grid
// (and, on single-logger sites, ESS charge/discharge) fields.
func (a *sampleAccumulator) addLogger(samples []HistorySample, windowStart, windowEnd time.Time) {
	for _, s := range samples {
		if !inWindow(s.Time, windowStart, windowEnd) {
			continue
		}
		ms := s.Time.UnixMilli()
		for field, metric := range metricKeyByLoggerField {
			// On dual-logger sites the ESS counters come from the
			// dedicated ESS logger, not the site logger (which reports
			// them as zero).
			if a.topo.EssLogger != nil && (field == "total_charge" || field == "total_discharge") {
				continue
			}
			if v, ok := s.Fields[field]; ok {
				a.values[cellKey{metric: metric, ms: ms}] = v
			}
		}
	}
}

// addEssLogger maps the dedicated ESS SmartLogger's charge/discharge
// counters (dual-logger sites only), preferring the plain total_* field
// and falling back to the AC-side counter.
func (a *sampleAccumulator) addEssLogger(samples []HistorySample, windowStart, windowEnd time.Time) {
	for _, s := range samples {
		if !inWindow(s.Time, windowStart, windowEnd) {
			continue
		}
		ms := s.Time.UnixMilli()
		if v, ok := firstField(s.Fields, essChargeFields); ok {
			a.values[cellKey{metric: "total_energy_charged_kwh", ms: ms}] = v
		}
		if v, ok := firstField(s.Fields, essDischargeFields); ok {
			a.values[cellKey{metric: "total_energy_discharged_kwh", ms: ms}] = v
		}
	}
}

// addEssDevice accumulates battery_soc from one ESS pack for later
// averaging across packs at each timestamp.
func (a *sampleAccumulator) addEssDevice(samples []HistorySample, windowStart, windowEnd time.Time) {
	for _, s := range samples {
		if !inWindow(s.Time, windowStart, windowEnd) {
			continue
		}
		if v, ok := s.Fields[essSocField]; ok {
			ms := s.Time.UnixMilli()
			a.socSum[ms] += v
			a.socCount[ms]++
		}
	}
}

// adjustPurePVYield rewrites accumulated_pv_energy_yield_kwh on single-
// SmartLogger hybrid sites. FusionSolar's total_yield counts cumulative
// inverter AC output (PV + ESS discharge), while live Modbus 40446 and
// getKpiStationDay's PVYield track pure PV only. Subtracting the co-
// located total_discharge counter at each timestamp recovers the correct
// series. Dual-logger sites (EssLogger != nil) skip this — their primary
// logger's total_yield is already PV-only and ESS discharge lives on the
// dedicated ESS logger.
func (a *sampleAccumulator) adjustPurePVYield() {
	if a.topo.EssLogger != nil {
		return
	}
	const pvKey = "accumulated_pv_energy_yield_kwh"
	const disKey = "total_energy_discharged_kwh"

	disByMS := map[int64]float64{}
	disMS := make([]int64, 0)
	for c, v := range a.values {
		if c.metric == disKey {
			disByMS[c.ms] = v
			disMS = append(disMS, c.ms)
		}
	}
	sort.Slice(disMS, func(i, j int) bool { return disMS[i] < disMS[j] })

	dischargeAt := func(ms int64) float64 {
		if v, ok := disByMS[ms]; ok {
			return v
		}
		i := sort.Search(len(disMS), func(i int) bool { return disMS[i] > ms }) - 1
		if i < 0 {
			return 0
		}
		return disByMS[disMS[i]]
	}

	type pvCell struct {
		key cellKey
		ms  int64
	}
	pvCells := make([]pvCell, 0)
	for c := range a.values {
		if c.metric == pvKey {
			pvCells = append(pvCells, pvCell{key: c, ms: c.ms})
		}
	}
	sort.Slice(pvCells, func(i, j int) bool { return pvCells[i].ms < pvCells[j].ms })

	var prevPure float64
	for _, pc := range pvCells {
		yield := a.values[pc.key]
		pure := yield - dischargeAt(pc.ms)
		if pure < 0 {
			pure = 0
		}
		// Both inputs are monotonic lifetime counters; keep the derived
		// pure-PV series monotonic too so delta aggregation never sees a
		// backward step from a transient discharge > yield glitch.
		if pure < prevPure {
			pure = prevPure
		}
		a.values[pc.key] = pure
		prevPure = pure
	}
}

// samples folds the averaged SOC into the value set and returns the
// final sample slice in stable (time, metric_key) order. Every sample
// carries the org's device_host label when known so the energy-flow
// allocator classifies archive rows exactly as live ones.
func (a *sampleAccumulator) samples() []storage.Sample {
	a.adjustPurePVYield()

	for ms, count := range a.socCount {
		if count == 0 {
			continue
		}
		a.values[cellKey{metric: socMetricKey, ms: ms}] = a.socSum[ms] / float64(count)
	}

	out := make([]storage.Sample, 0, len(a.values))
	for c, v := range a.values {
		// Each sample carries its own labels map (CopyFrom marshals
		// per-row). The `source` tag marks the row as archive so the
		// idempotency delete is scoped to it and live data is never
		// touched; `device_host` mirrors the live collector — resolved
		// per metric_key so dual-SmartLogger sites split PV/grid/load
		// and ESS counters onto the same hosts live data uses, and the
		// energy-flow allocator classifies archive rows identically.
		l := map[string]string{SourceLabel: SourceValue}
		if host := a.hosts.hostFor(c.metric); host != "" {
			l["device_host"] = host
		}
		out = append(out, storage.Sample{
			Time:           time.UnixMilli(c.ms).UTC(),
			OrganizationID: a.orgID,
			MetricKey:      c.metric,
			Value:          v,
			Labels:         l,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Time.Equal(out[j].Time) {
			return out[i].MetricKey < out[j].MetricKey
		}
		return out[i].Time.Before(out[j].Time)
	})
	return out
}

// inWindow keeps a sample only when it falls inside the half-open
// device window [windowStart, windowEnd). Half-open everywhere means a
// sample on a 24h chunk boundary is attributed to exactly one window
// (no duplicates), and the closing instant of the overall request
// (which equals the archive cutoff on a max-range import) is never
// written — so an archive run can never place a row at or past the
// live-data boundary.
func inWindow(t, windowStart, windowEnd time.Time) bool {
	return !t.Before(windowStart) && t.Before(windowEnd)
}

// firstField returns the first present field from `prefer`, in order.
func firstField(fields map[string]float64, prefer []string) (float64, bool) {
	for _, k := range prefer {
		if v, ok := fields[k]; ok {
			return v, true
		}
	}
	return 0, false
}
