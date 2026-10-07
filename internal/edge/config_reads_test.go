package edge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nesh/sestelemetry/internal/registers"
)

func TestResolveDeviceEntriesMeterAndOptional(t *testing.T) {
	dir := t.TempDir()
	src, err := os.ReadFile("../../registers/huawei_smartlogger.yaml")
	if err != nil {
		t.Fatal(err)
	}
	catPath := filepath.Join(dir, "catalog.yaml")
	if err := os.WriteFile(catPath, src, 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testCfg()
	cfg.RegisterCatalog = catPath
	cfg.SmartLogger.Topology = TopologyDual
	uid := 99
	cfg.SmartLogger.Devices = []Device{
		{Role: RolePV, Host: "mock-pv", UnitID: &uid, Meter: &MeterConfig{UnitID: 11}},
		{Role: RoleESS, Host: "mock-ess", UnitID: &uid},
	}
	got, err := resolveDeviceEntries(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pv, ess := got[RolePV], got[RoleESS]
	if len(pv.meter) != len(MeterMetricKeys) {
		t.Fatalf("pv meter keys = %d, want %d", len(pv.meter), len(MeterMetricKeys))
	}
	if len(ess.meter) != 0 {
		t.Fatalf("ess must not read the meter, got %d keys", len(ess.meter))
	}
	if !hasKey(pv.optional, "pv_active_power_adjustment_kw") || !hasKey(pv.optional, "grid_line_voltage_ab_v") {
		t.Fatalf("pv optional missing cutover keys")
	}
	if !hasKey(ess.optional, "ess_active_power_adjustment_kw") || !hasKey(ess.optional, "sl_alarm_7") {
		t.Fatalf("ess optional missing cutover keys")
	}
	if hasKey(ess.optional, "sl_alarm_1") {
		t.Fatal("alarm 1..6 belong on the required ESS poll, not optional")
	}
}

func hasKey(entries []registers.ResolvedEntry, key string) bool {
	for _, e := range entries {
		if e.MetricKey == key {
			return true
		}
	}
	return false
}
