package inventory_test

import (
	"testing"
	"time"

	"github.com/nesh/sestelemetry/internal/energyflow"
	"github.com/nesh/sestelemetry/internal/inventory"
)

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

func TestMergeDualSL(t *testing.T) {
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
	snap := inventory.Merge("ze", inventory.PollReasonStartup, time.Unix(1_700_000_000, 0).UTC(), []inventory.DeviceReading{pv, ess})
	if snap.DeviceHost != "" {
		t.Fatalf("merged snapshot should have empty device_host, got %q", snap.DeviceHost)
	}
	if snap.PVRatedKw == nil || *snap.PVRatedKw != 600 {
		t.Fatalf("pv: %#v", snap.PVRatedKw)
	}
	if snap.ESSCount == nil || *snap.ESSCount != 8 {
		t.Fatalf("ess_count: %#v", snap.ESSCount)
	}
	if len(snap.QualityFlags) != 0 {
		t.Fatalf("unexpected flags: %v", snap.QualityFlags)
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
	snap := inventory.Merge("ab", inventory.PollReasonHourly, time.Now().UTC(), []inventory.DeviceReading{r})
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

func TestCoalesceLatestBackfillsZerosAndNulls(t *testing.T) {
	t0 := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	t1 := t0.Add(10 * time.Hour) // night poll
	v := func(x float64) *float64 { return &x }
	// Newest first, as storage returns them.
	snaps := []inventory.Snapshot{
		{Time: t1, PollReason: inventory.PollReasonHourly, PVRatedKw: v(0), ESSRatedKwh: nil, ActivePowerControlMode: v(4)},
		{Time: t0, PollReason: inventory.PollReasonDaily, PVRatedKw: v(550), ESSRatedKwh: v(645), ActivePowerControlMode: v(4)},
	}
	out, ok := inventory.CoalesceLatest(snaps)
	if !ok {
		t.Fatal("expected ok")
	}
	if out.Time != t1 || out.PollReason != inventory.PollReasonHourly {
		t.Fatalf("meta must come from newest snapshot: %#v", out)
	}
	if out.PVRatedKw == nil || *out.PVRatedKw != 550 {
		t.Fatalf("pv_rated_kw should backfill 550 over night-time 0: %#v", out.PVRatedKw)
	}
	if out.ESSRatedKwh == nil || *out.ESSRatedKwh != 645 {
		t.Fatalf("ess_rated_kwh should backfill over nil: %#v", out.ESSRatedKwh)
	}
	if out.ActivePowerControlMode == nil || *out.ActivePowerControlMode != 4 {
		t.Fatalf("control mode: %#v", out.ActivePowerControlMode)
	}
}

func TestCoalesceLatestEmpty(t *testing.T) {
	if _, ok := inventory.CoalesceLatest(nil); ok {
		t.Fatal("expected ok=false for empty input")
	}
}
