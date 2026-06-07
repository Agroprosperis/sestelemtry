package api

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
)

// TestEnergyFlowSources_LiveCSVReductionEquivalent replays a real day
// of per-second telemetry exported from the live /samples endpoint and
// proves the per-minute SQL reduction is lossless on production data
// shapes (real cadence jitter, real sentinels, the dual-SmartLogger
// .101/.102 split). It is skipped unless EF_LIVE_CSV points at a CSV
// in the /samples export format:
//
//	time,metric_key,modbus_register,data_type,gain,value,labels
//
// Run with:
//
//	EF_LIVE_CSV=/tmp/ze_2026-05-31.csv go test ./internal/api/ \
//	  -run TestEnergyFlowSources_LiveCSVReductionEquivalent -v
func TestEnergyFlowSources_LiveCSVReductionEquivalent(t *testing.T) {
	path := os.Getenv("EF_LIVE_CSV")
	if path == "" {
		t.Skip("set EF_LIVE_CSV to a /samples CSV export to run the live equivalence check")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	records, err := r.ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(records) < 2 {
		t.Fatalf("csv has no data rows")
	}

	raw := make([]EnergyFlowRawRow, 0, len(records)-1)
	for i, rec := range records {
		if i == 0 {
			continue // header
		}
		if len(rec) < 7 {
			continue
		}
		ts, perr := time.Parse(time.RFC3339Nano, rec[0])
		if perr != nil {
			t.Fatalf("row %d: parse time %q: %v", i, rec[0], perr)
		}
		val, serr := strconv.ParseFloat(rec[5], 64)
		if serr != nil {
			t.Fatalf("row %d: parse value %q: %v", i, rec[5], serr)
		}
		host := ""
		if rec[6] != "" {
			var labels map[string]string
			if jerr := json.Unmarshal([]byte(rec[6]), &labels); jerr == nil {
				host = labels["device_host"]
			}
		}
		raw = append(raw, EnergyFlowRawRow{
			Time:       ts.UTC(),
			MetricKey:  rec[1],
			Value:      val,
			DeviceHost: host,
		})
	}
	// /samples returns DESC; the allocator wants monotonic ASC input,
	// which buildRawSamples + Recompute tolerate via internal bucketing,
	// but sort anyway so the reduced/raw inputs are on equal footing.
	sortRowsByTimeMetric(raw)

	// Reproduce the production dual-SmartLogger role map for org "ze":
	// the .101 logger carries the PV/grid counters, the .102 logger the
	// ESS charge/discharge counters. The exact roles matter only for
	// matching the dashboard's numbers — the equivalence assertion holds
	// for any fixed cfg because both runs share it.
	cfg := EnergyFlowOrg{
		ID:               "ze",
		EssDischargeSign: 1,
		Devices: []EnergyFlowDevice{
			{Host: "10.28.40.101", Role: string(energyflow.RolePV)},
			{Host: "10.28.40.102", Role: string(energyflow.RoleESS)},
		},
	}

	rawTotals := recomputeTotals(raw, cfg)
	reduced := reduceToCeilMinuteLast(raw)
	reducedTotals := recomputeTotals(reduced, cfg)

	t.Logf("rows raw=%d reduced=%d (%.1fx)", len(raw), len(reduced), float64(len(raw))/float64(len(reduced)))
	t.Logf("raw     totals: pv_to_ess=%.4f grid_to_ess=%.4f ess_to_load=%.4f ess_to_grid=%.4f",
		rawTotals[energyflow.MetricPVToESSKwh], rawTotals[energyflow.MetricGridToESSKwh],
		rawTotals[energyflow.MetricESSToLoadKwh], rawTotals[energyflow.MetricESSToGridKwh])
	t.Logf("reduced totals: pv_to_ess=%.4f grid_to_ess=%.4f ess_to_load=%.4f ess_to_grid=%.4f",
		reducedTotals[energyflow.MetricPVToESSKwh], reducedTotals[energyflow.MetricGridToESSKwh],
		reducedTotals[energyflow.MetricESSToLoadKwh], reducedTotals[energyflow.MetricESSToGridKwh])

	const tol = 1e-6
	for _, m := range []string{
		energyflow.MetricPVToESSKwh,
		energyflow.MetricGridToESSKwh,
		energyflow.MetricESSToLoadKwh,
		energyflow.MetricESSToGridKwh,
	} {
		if !floatNearAPI(rawTotals[m], reducedTotals[m], tol) {
			t.Fatalf("%s mismatch: raw=%.9f reduced=%.9f", m, rawTotals[m], reducedTotals[m])
		}
	}
}

func recomputeTotals(rows []EnergyFlowRawRow, cfg EnergyFlowOrg) map[string]float64 {
	samples := buildRawSamples(rows, cfg)
	rec := energyflow.Recompute(samples, energyflow.Options{
		EssDischargeSign:        cfg.EssDischargeSign,
		AllocationWindowSeconds: 60,
		MaxGapSeconds:           0,
	})
	return rec.Totals
}

func sortRowsByTimeMetric(rows []EnergyFlowRawRow) {
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Time.Equal(rows[j].Time) {
			return rows[i].Time.Before(rows[j].Time)
		}
		return rows[i].MetricKey < rows[j].MetricKey
	})
}
