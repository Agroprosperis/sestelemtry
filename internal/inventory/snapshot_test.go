package inventory_test

import (
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/config"
	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/inventory"
)

func f64(v float64) *float64 { return &v }

func TestKeysForRole(t *testing.T) {
	pv := inventory.KeysForRole(energyflow.RolePV)
	if len(pv) != 2 || pv[0] != inventory.MetricPVRatedKw {
		t.Fatalf("PV keys: %#v", pv)
	}
	ess := inventory.KeysForRole(energyflow.RoleESS)
	if len(ess) != 6 {
		t.Fatalf("ESS keys len=%d %#v", len(ess), ess)
	}
	all := inventory.KeysForRole(energyflow.RoleSingle)
	if len(all) != len(inventory.AllMetricKeys) {
		t.Fatalf("single keys: %#v", all)
	}
}

func TestIsInventoryMetricKey(t *testing.T) {
	if !inventory.IsInventoryMetricKey(inventory.MetricPVRatedKw) {
		t.Fatal("expected pv_rated_kw to be inventory")
	}
	if inventory.IsInventoryMetricKey("soc_percent") {
		t.Fatal("soc_percent must stay in telemetry")
	}
}

func TestMergeDualSLAndMismatch(t *testing.T) {
	pv := inventory.ReadingFromValues("10.0.0.1", map[string]float64{
		inventory.MetricPVRatedKw:              600,
		inventory.MetricActivePowerControlMode: 4,
	})
	ess := inventory.ReadingFromValues("10.0.0.2", map[string]float64{
		inventory.MetricESSRatedKw:             864,
		inventory.MetricESSRatedKwh:            1720,
		inventory.MetricESSCount:               8,
		inventory.MetricPCSCount:               8,
		inventory.MetricESSSOHPct:              99.5,
		inventory.MetricActivePowerControlMode: 4,
	})
	expected := &config.InventoryExpected{
		PVRatedKw:   f64(600),
		ESSRatedKw:  f64(800), // mismatch
		ESSRatedKwh: f64(1720),
		ESSCount:    f64(8),
	}
	snap := inventory.Merge("ze", inventory.PollReasonStartup, time.Unix(1_700_000_000, 0).UTC(), []inventory.DeviceReading{pv, ess}, expected)
	if snap.DeviceHost != "" {
		t.Fatalf("merged snapshot should have empty device_host, got %q", snap.DeviceHost)
	}
	if snap.PVRatedKw == nil || *snap.PVRatedKw != 600 {
		t.Fatalf("pv: %#v", snap.PVRatedKw)
	}
	if snap.ESSCount == nil || *snap.ESSCount != 8 {
		t.Fatalf("ess_count: %#v", snap.ESSCount)
	}
	found := false
	for _, f := range snap.QualityFlags {
		if f == inventory.FlagInventoryMismatch {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected INVENTORY_MISMATCH, flags=%v raw=%v", snap.QualityFlags, snap.Raw)
	}
}

func TestMergeControlModeNotRemote(t *testing.T) {
	r := inventory.ReadingFromValues("10.0.0.1", map[string]float64{
		inventory.MetricPVRatedKw:              450,
		inventory.MetricESSRatedKw:             324,
		inventory.MetricESSRatedKwh:            645,
		inventory.MetricESSCount:               3,
		inventory.MetricActivePowerControlMode: 0,
	})
	snap := inventory.Merge("ab", inventory.PollReasonHourly, time.Now().UTC(), []inventory.DeviceReading{r}, nil)
	found := false
	for _, f := range snap.QualityFlags {
		if f == inventory.FlagControlModeNotRemote {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected CONTROL_MODE_NOT_REMOTE, flags=%v", snap.QualityFlags)
	}
	if snap.DeviceHost != "10.0.0.1" {
		t.Fatalf("single-device host: %q", snap.DeviceHost)
	}
}
