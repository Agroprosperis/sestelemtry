package registers

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestResolveWithBaseOverride(t *testing.T) {
	c := &Catalog{
		Registers: []Entry{
			{
				MetricKey: "grid_connected_active_power_kw",
				Address:   440505,
				DataType:  DTInt32,
				SwapType:  SwapABCD_BE,
				Gain:      0.001,
			},
		},
	}
	c.Addressing.HoldingAddressBase = 400001

	r, err := c.Resolve(400001)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}
	if len(r) != 1 {
		t.Fatalf("want 1 resolved entry, got %d", len(r))
	}
	if r[0].PDUStart != 40504 || r[0].WordCount != 2 || r[0].PDUEnd != 40505 {
		t.Fatalf("unexpected resolved entry: %+v", r[0])
	}
}

func TestResolveRejectsUnsupportedSwapType(t *testing.T) {
	c := &Catalog{
		Registers: []Entry{
			{MetricKey: "m", Address: 400010, DataType: DTUint16, SwapType: "CDAB"},
		},
	}
	if _, err := c.Resolve(400001); err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestHuaweiCatalogResolvesAllKeys guards against accidental regressions in
// `registers/huawei_smartlogger.yaml`: every entry must resolve, every key
// must be unique, and PDU ranges must not overlap. In particular the new
// "*_day_kwh" counters (40438, 40468, 40470, 40509, 40511, 40513) must
// coexist with the surrounding 4-word INT64 totals.
func TestHuaweiCatalogResolvesAllKeys(t *testing.T) {
	cat, err := Load("../../registers/huawei_smartlogger.yaml")
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	resolved, err := cat.Resolve(0)
	if err != nil {
		t.Fatalf("Resolve error: %v", err)
	}

	required := []string{
		"power_supply_from_grid_day_kwh",
		"energy_charged_day_kwh",
		"energy_discharged_day_kwh",
		"power_consumption_day_kwh",
		"electricity_sold_day_kwh",
		"electricity_purchased_day_kwh",
	}
	keys := make(map[string]ResolvedEntry, len(resolved))
	for _, e := range resolved {
		if _, dup := keys[e.MetricKey]; dup {
			t.Fatalf("duplicate metric_key %q", e.MetricKey)
		}
		keys[e.MetricKey] = e
	}
	for _, k := range required {
		if _, ok := keys[k]; !ok {
			t.Fatalf("missing required metric_key %q", k)
		}
	}

	sort.Slice(resolved, func(i, j int) bool { return resolved[i].PDUStart < resolved[j].PDUStart })
	for i := 1; i < len(resolved); i++ {
		prev := resolved[i-1]
		cur := resolved[i]
		if cur.PDUStart <= prev.PDUEnd {
			t.Fatalf("PDU overlap between %q (%d..%d) and %q (%d..%d)",
				prev.MetricKey, prev.PDUStart, prev.PDUEnd,
				cur.MetricKey, cur.PDUStart, cur.PDUEnd)
		}
	}
}

func TestSubsetFiltersAndPreservesOrder(t *testing.T) {
	all := []ResolvedEntry{
		{Entry: Entry{MetricKey: "a"}},
		{Entry: Entry{MetricKey: "b"}},
		{Entry: Entry{MetricKey: "c"}},
		{Entry: Entry{MetricKey: "d"}},
	}
	got, err := Subset(all, []string{"c", "a"})
	if err != nil {
		t.Fatalf("Subset error: %v", err)
	}
	if len(got) != 2 || got[0].MetricKey != "a" || got[1].MetricKey != "c" {
		t.Fatalf("unexpected subset result: %+v", got)
	}
}

func TestSubsetEmptyKeysReturnsAll(t *testing.T) {
	all := []ResolvedEntry{
		{Entry: Entry{MetricKey: "a"}},
		{Entry: Entry{MetricKey: "b"}},
	}
	got, err := Subset(all, nil)
	if err != nil {
		t.Fatalf("Subset error: %v", err)
	}
	if len(got) != len(all) {
		t.Fatalf("expected pass-through, got %d entries", len(got))
	}
}

func TestSubsetUnknownKeyError(t *testing.T) {
	all := []ResolvedEntry{
		{Entry: Entry{MetricKey: "a"}},
	}
	_, err := Subset(all, []string{"a", "missing", "also_missing"})
	if err == nil {
		t.Fatal("expected error for unknown keys")
	}
	msg := err.Error()
	if !strings.Contains(msg, "missing") || !strings.Contains(msg, "also_missing") {
		t.Fatalf("error should list both missing keys: %v", err)
	}
}

func TestLoadTrimsMetricKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.yaml")
	content := `addressing:
  holding_address_base: 400001
registers:
  - metric_key: "  metric_x  "
    name: X
    address: 440388
    data_type: UINT32
    swap_type: ABCD_BE
    gain: 0.001
    offset: 0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if c.Registers[0].MetricKey != "metric_x" {
		t.Fatalf("metric key was not trimmed: %q", c.Registers[0].MetricKey)
	}
}
