package fusionsolar

import (
	"sort"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

// sampleAccumulator folds FusionSolar device history into the
// deduplicated set of telemetry_samples the importer writes. It is the
// pure transform half of Import (no network, no DB) so the cumulative
// field mapping and SOC averaging can be unit-tested in isolation.
//
// Values are keyed by (metric_key, unix-milli) so a sample appearing at
// a window boundary in two successive 24h fetches collapses to one row.
type sampleAccumulator struct {
	orgID string
	host  string
	topo  PlantTopology

	values   map[cellKey]float64
	socSum   map[int64]float64
	socCount map[int64]int
}

type cellKey struct {
	metric string
	ms     int64
}

func newSampleAccumulator(orgID, host string, topo PlantTopology) *sampleAccumulator {
	return &sampleAccumulator{
		orgID:    orgID,
		host:     host,
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

// samples folds the averaged SOC into the value set and returns the
// final sample slice in stable (time, metric_key) order. Every sample
// carries the org's device_host label when known so the energy-flow
// allocator classifies archive rows exactly as live ones.
func (a *sampleAccumulator) samples() []storage.Sample {
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
		// touched; `device_host` mirrors the live collector so the
		// energy-flow allocator classifies archive rows identically.
		l := map[string]string{SourceLabel: SourceValue}
		if a.host != "" {
			l["device_host"] = a.host
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
