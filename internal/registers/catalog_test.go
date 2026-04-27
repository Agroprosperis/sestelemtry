package registers

import (
	"os"
	"path/filepath"
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
