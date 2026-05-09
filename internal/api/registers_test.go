package api

import (
	"math"
	"testing"

	"github.com/nesh/sestelemetry/internal/registers"
)

// TestModbusRegisterMetadataMatchesCatalog is the drift guard for
// the hand-maintained `ModbusRegisterMetadata` map: every entry must
// agree with `registers/huawei_smartlogger.yaml` (the catalog the
// collector actually uses). A vendor catalog change without a
// matching API update fails this test rather than quietly leaking
// stale addresses into exported CSVs.
//
// Synthetic metrics (DAM price, PV forecast) are absent from both
// sides by design — the test only asserts presence/equality for
// metrics that exist in the catalog AND in the API map.
func TestModbusRegisterMetadataMatchesCatalog(t *testing.T) {
	cat, err := registers.Load("../../registers/huawei_smartlogger.yaml")
	if err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	yaml := make(map[string]registers.Entry, len(cat.Registers))
	for _, e := range cat.Registers {
		yaml[e.MetricKey] = e
	}
	for key, meta := range ModbusRegisterMetadata {
		entry, ok := yaml[key]
		if !ok {
			t.Errorf("metric %q present in API map but absent from catalog YAML", key)
			continue
		}
		if entry.Address != meta.Address {
			t.Errorf("%s: API address=%d, YAML address=%d", key, meta.Address, entry.Address)
		}
		if string(entry.DataType) != meta.DataType {
			t.Errorf("%s: API data_type=%s, YAML data_type=%s", key, meta.DataType, entry.DataType)
		}
		if math.Abs(entry.Gain-meta.Gain) > 1e-12 {
			t.Errorf("%s: API gain=%v, YAML gain=%v", key, meta.Gain, entry.Gain)
		}
	}
	// The reverse check is also important: a new metric added to the
	// catalog should be advertised through /api/v1/registers so the
	// export annotates it. Skipping a key is fine for non-exported
	// metrics, but we want explicit visibility instead of a silent
	// gap, so any catalog-only key is reported as a warning here.
	for key := range yaml {
		if _, ok := ModbusRegisterMetadata[key]; !ok {
			t.Logf("metric %q exists in catalog but is not exposed via ModbusRegisterMetadata; add it if it's exported", key)
		}
	}
}
