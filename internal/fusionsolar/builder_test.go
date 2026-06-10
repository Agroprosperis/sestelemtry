package fusionsolar

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/storage"
)

func ts(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t.UTC()
}

func valueAt(samples []storage.Sample, metric string, t time.Time) (float64, bool) {
	for _, s := range samples {
		if s.MetricKey == metric && s.Time.Equal(t) {
			return s.Value, true
		}
	}
	return 0, false
}

// TestSingleLoggerMapping verifies the cumulative SmartLogger fields map
// to the dashboard metric_keys verbatim, with the device_host label.
func TestSingleLoggerMapping(t *testing.T) {
	topo := Topology["sm"]
	acc := newSampleAccumulator("sm", "10.36.40.102", topo)

	start := ts("2026-06-02T14:50:00Z")
	end := start.Add(maxHistoryWindow)
	t0 := ts("2026-06-02T15:00:00Z")

	acc.addLogger([]HistorySample{
		{Time: t0, Fields: map[string]float64{
			"total_yield":             36842.65,
			"total_power_consumption": 68193.29,
			"total_supply_from_grid":  39323.16,
			"total_feed_in_to_grid":   19.78,
			"total_charge":            7952.7,
			"total_discharge":         6492.31,
		}},
	}, start, end)

	out := acc.samples()

	cases := map[string]float64{
		"accumulated_pv_energy_yield_kwh":       36842.65,
		"accumulated_power_consumption_kwh":     68193.29,
		"accumulated_electricity_purchased_kwh": 39323.16,
		"accumulated_electricity_sold_kwh":      19.78,
		"total_energy_charged_kwh":              7952.7,
		"total_energy_discharged_kwh":           6492.31,
	}
	for metric, want := range cases {
		got, ok := valueAt(out, metric, t0)
		if !ok {
			t.Fatalf("missing metric %s at %s", metric, t0)
		}
		if got != want {
			t.Errorf("metric %s = %v, want %v", metric, got, want)
		}
	}
	for _, s := range out {
		if s.Labels["device_host"] != "10.36.40.102" {
			t.Errorf("sample %s missing device_host label: %v", s.MetricKey, s.Labels)
		}
		if s.Labels[SourceLabel] != SourceValue {
			t.Errorf("sample %s missing %s=%s label: %v", s.MetricKey, SourceLabel, SourceValue, s.Labels)
		}
	}
}

// TestEssSocAveraging verifies SOC is averaged across battery packs at
// each timestamp and emitted as soc_percent.
func TestEssSocAveraging(t *testing.T) {
	acc := newSampleAccumulator("sm", "", Topology["sm"])

	start := ts("2026-06-02T14:50:00Z")
	end := start.Add(maxHistoryWindow)
	t0 := ts("2026-06-02T15:00:00Z")

	acc.addEssDevice([]HistorySample{{Time: t0, Fields: map[string]float64{"battery_soc": 92}}}, start, end)
	acc.addEssDevice([]HistorySample{{Time: t0, Fields: map[string]float64{"battery_soc": 88}}}, start, end)
	acc.addEssDevice([]HistorySample{{Time: t0, Fields: map[string]float64{"battery_soc": 90}}}, start, end)

	out := acc.samples()
	got, ok := valueAt(out, "soc_percent", t0)
	if !ok {
		t.Fatal("missing soc_percent")
	}
	if got != 90 {
		t.Errorf("soc_percent = %v, want 90", got)
	}
}

// TestDualLoggerEssCounters verifies that on a dual-logger site the ESS
// charge/discharge come from the dedicated ESS logger and the primary
// logger's (zero) ESS counters are ignored.
func TestDualLoggerEssCounters(t *testing.T) {
	topo := Topology["ze"]
	if topo.EssLogger == nil {
		t.Fatal("expected ze to be a dual-logger topology")
	}
	acc := newSampleAccumulator("ze", "", topo)

	start := ts("2026-06-02T14:50:00Z")
	end := start.Add(maxHistoryWindow)
	t0 := ts("2026-06-02T15:00:00Z")

	acc.addLogger([]HistorySample{{Time: t0, Fields: map[string]float64{
		"total_yield":     1192659.1,
		"total_charge":    0,
		"total_discharge": 0,
	}}}, start, end)
	acc.addEssLogger([]HistorySample{{Time: t0, Fields: map[string]float64{
		"total_charge":    143348.99,
		"total_discharge": 128383.2,
	}}}, start, end)

	out := acc.samples()
	if v, _ := valueAt(out, "total_energy_charged_kwh", t0); v != 143348.99 {
		t.Errorf("total_energy_charged_kwh = %v, want 143348.99", v)
	}
	if v, _ := valueAt(out, "total_energy_discharged_kwh", t0); v != 128383.2 {
		t.Errorf("total_energy_discharged_kwh = %v, want 128383.2", v)
	}
	if v, _ := valueAt(out, "accumulated_pv_energy_yield_kwh", t0); v != 1192659.1 {
		t.Errorf("accumulated_pv_energy_yield_kwh = %v, want 1192659.1", v)
	}
}

// TestAcChargeFallback verifies ac_total_charge_energy is used when the
// plain total_charge field is absent on the ESS logger.
func TestAcChargeFallback(t *testing.T) {
	acc := newSampleAccumulator("ze", "", Topology["ze"])
	start := ts("2026-06-02T14:50:00Z")
	end := start.Add(maxHistoryWindow)
	t0 := ts("2026-06-02T15:00:00Z")

	acc.addEssLogger([]HistorySample{{Time: t0, Fields: map[string]float64{
		"ac_total_charge_energy": 143348.99,
	}}}, start, end)

	out := acc.samples()
	if v, ok := valueAt(out, "total_energy_charged_kwh", t0); !ok || v != 143348.99 {
		t.Errorf("ac fallback total_energy_charged_kwh = %v ok=%v, want 143348.99", v, ok)
	}
}

// TestWindowDedup verifies a sample on the boundary of two successive
// windows is written once (the half-open window rule).
func TestWindowDedup(t *testing.T) {
	acc := newSampleAccumulator("sm", "", Topology["sm"])

	w1Start := ts("2026-06-02T14:50:00Z")
	w1End := w1Start.Add(maxHistoryWindow)
	w2Start := w1End
	w2End := w2Start.Add(maxHistoryWindow)

	boundary := w1End
	acc.addLogger([]HistorySample{{Time: boundary, Fields: map[string]float64{"total_yield": 100}}}, w1Start, w1End)
	acc.addLogger([]HistorySample{{Time: boundary, Fields: map[string]float64{"total_yield": 100}}}, w2Start, w2End)

	out := acc.samples()
	count := 0
	for _, s := range out {
		if s.MetricKey == "accumulated_pv_energy_yield_kwh" && s.Time.Equal(boundary) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("boundary sample written %d times, want 1", count)
	}
}

// TestParseHistoryRow covers the nested dataItemMap shape, the flat
// shape, numeric-string values, and null handling.
func TestParseHistoryRow(t *testing.T) {
	nested := json.RawMessage(`{"collectTime":1780411800000,"dataItemMap":{"total_yield":127959.26,"total_charge":null,"battery_soc":"90.0"}}`)
	s, ok, err := parseHistoryRow(nested)
	if err != nil || !ok {
		t.Fatalf("nested parse failed: ok=%v err=%v", ok, err)
	}
	if s.Fields["total_yield"] != 127959.26 {
		t.Errorf("total_yield = %v", s.Fields["total_yield"])
	}
	if _, present := s.Fields["total_charge"]; present {
		t.Errorf("null total_charge should be dropped")
	}
	if s.Fields["battery_soc"] != 90.0 {
		t.Errorf("string battery_soc = %v, want 90", s.Fields["battery_soc"])
	}
	if want := time.UnixMilli(1780411800000).UTC(); !s.Time.Equal(want) {
		t.Errorf("time = %v, want %v", s.Time, want)
	}

	flat := json.RawMessage(`{"collectTime":1780411800000,"total_yield":1.5}`)
	s2, ok, err := parseHistoryRow(flat)
	if err != nil || !ok {
		t.Fatalf("flat parse failed: ok=%v err=%v", ok, err)
	}
	if s2.Fields["total_yield"] != 1.5 {
		t.Errorf("flat total_yield = %v, want 1.5", s2.Fields["total_yield"])
	}

	// dataItems is the shape the live eu5 OpenAPI returns (verified
	// against the real endpoint); nulls must be dropped.
	items := json.RawMessage(`{"devDn":"NE=179121695","collectTime":1777593600000,"dataItems":{"total_yield":419213.66,"ac_total_discharge_energy":null,"total_charge":159145.31}}`)
	s3, ok, err := parseHistoryRow(items)
	if err != nil || !ok {
		t.Fatalf("dataItems parse failed: ok=%v err=%v", ok, err)
	}
	if s3.Fields["total_yield"] != 419213.66 {
		t.Errorf("dataItems total_yield = %v, want 419213.66", s3.Fields["total_yield"])
	}
	if s3.Fields["total_charge"] != 159145.31 {
		t.Errorf("dataItems total_charge = %v, want 159145.31", s3.Fields["total_charge"])
	}
	if _, present := s3.Fields["ac_total_discharge_energy"]; present {
		t.Errorf("null ac_total_discharge_energy should be dropped")
	}
	if _, present := s3.Fields["devDn"]; present {
		t.Errorf("devDn must not be parsed as a field")
	}
}
