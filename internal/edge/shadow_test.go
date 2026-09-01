package edge

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

var testTS = time.Date(2026, 6, 4, 12, 30, 0, 0, time.UTC)

func testCfg() *Config {
	return &Config{
		SiteID:   "ab",
		Timezone: "Europe/Kyiv",
		SmartLogger: SmartLogger{
			Topology:     TopologySingle,
			PollInterval: time.Second,
			Devices:      []Device{{Role: RoleAll, Host: "mock"}},
		},
		Edge:    EdgeIdentity{EdgeID: "iot2050-test"},
		Control: ControlConfig{Mode: ModeShadow, Preset: PresetSelfConsumption, Interval: time.Second},
		Limits: Limits{
			Grid: GridLimits{ImportLimitKw: 947, TargetImportKw: 900},
			PV:   PVLimits{RatedKw: 450},
			Bess: BessLimits{
				RatedPowerKw: 324, RatedCapacityKwh: 645,
				SocMinEconomicPct: 20, SocMaxEconomicPct: 90,
			},
		},
	}
}

func testTick(values map[string]float64, quality string) Tick {
	return buildTickFromValues("ab", TopologySingle, 1, testTS, values, quality)
}

func arbitrageManifest(planKw float64) *Manifest {
	return &Manifest{
		SchemaVersion: ManifestSchemaLite,
		ManifestID:    "ab-test-1",
		SiteID:        "ab",
		ValidFrom:     testTS.Add(-time.Hour),
		ValidUntil:    testTS.Add(12 * time.Hour),
		Mode:          ModeShadow,
		Preset:        PresetEconomicArbitrage,
		SocPolicy:     SocPolicy{MinEconomicPct: 20, MaxEconomicPct: 90},
		GridLimits:    ManifestGridLimits{TargetImportKw: 900, PvRatedKw: 450},
		Plan: &Plan{
			Granularity: "1h",
			Intervals: []PlanInterval{
				{TS: testTS.Truncate(time.Hour), EssKw: planKw, PriceUah: 5.5},
			},
		},
	}
}

func TestSelfConsumptionChargesFromSurplus(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 300, "load_power_kw": 100, "soc_percent": 50,
	}, QualityOK)
	d, _ := Decide(tick, nil, testCfg())
	if d.ReasonCode != "self_charge" {
		t.Fatalf("reason = %s, want self_charge (%s)", d.ReasonCode, d.Rationale)
	}
	if d.PBessVirtualKw != -200 {
		t.Fatalf("p = %v, want -200", d.PBessVirtualKw)
	}
	if d.Degraded {
		t.Fatalf("unexpected degraded: %s", d.Rationale)
	}
}

func TestSelfConsumptionDischargesIntoDeficit(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 50, "load_power_kw": 250, "soc_percent": 50,
	}, QualityOK)
	d, _ := Decide(tick, nil, testCfg())
	if d.ReasonCode != "self_discharge" || d.PBessVirtualKw != 200 {
		t.Fatalf("got %s %v, want self_discharge 200", d.ReasonCode, d.PBessVirtualKw)
	}
}

func TestHoldWithinDeadband(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 101, "soc_percent": 50,
	}, QualityOK)
	d, _ := Decide(tick, nil, testCfg())
	if d.ReasonCode != "hold" || d.PBessVirtualKw != 0 {
		t.Fatalf("got %s %v, want hold 0", d.ReasonCode, d.PBessVirtualKw)
	}
}

func TestSocFloorBlocksDischarge(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 300, "soc_percent": 20,
	}, QualityOK)
	d, _ := Decide(tick, nil, testCfg())
	if d.PBessVirtualKw != 0 || !d.Degraded {
		t.Fatalf("got p=%v degraded=%v, want 0/true (%s)", d.PBessVirtualKw, d.Degraded, d.Rationale)
	}
}

func TestSocCeilingBlocksCharge(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 400, "load_power_kw": 100, "soc_percent": 90,
	}, QualityOK)
	d, _ := Decide(tick, nil, testCfg())
	if d.PBessVirtualKw != 0 || !d.Degraded {
		t.Fatalf("got p=%v degraded=%v, want 0/true", d.PBessVirtualKw, d.Degraded)
	}
}

func TestDynamicDischargeLimitClamps(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 400, "soc_percent": 60,
		"ess_discharge_max_kw": 150,
	}, QualityOK)
	d, evs := Decide(tick, nil, testCfg())
	if d.PBessVirtualKw != 150 || !d.Degraded {
		t.Fatalf("got p=%v degraded=%v, want 150/true (%s)", d.PBessVirtualKw, d.Degraded, d.Rationale)
	}
	found := false
	for _, ev := range evs {
		if ev.Code == EvDispatchDegrade {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected DISPATCH_DEGRADED event, got %v", evs)
	}
}

func TestPlanDischargeClampedToDeficitNoExport(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 50, "load_power_kw": 100, "soc_percent": 70,
	}, QualityOK)
	d, _ := Decide(tick, arbitrageManifest(200), testCfg())
	if d.ReasonCode != "plan_discharge" {
		t.Fatalf("reason = %s, want plan_discharge", d.ReasonCode)
	}
	if d.PBessVirtualKw != 50 {
		t.Fatalf("p = %v, want 50 (clamped to deficit)", d.PBessVirtualKw)
	}
	if d.PBessPlanKw == nil || *d.PBessPlanKw != 200 {
		t.Fatalf("plan input not recorded: %v", d.PBessPlanKw)
	}
}

func TestPlanGridChargeRespectsImportTarget(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 700, "soc_percent": 40,
	}, QualityOK)
	d, _ := Decide(tick, arbitrageManifest(-300), testCfg())
	// Headroom = 900 target − 700 load = 200 kW of grid charge.
	if d.PBessVirtualKw != -200 || !d.Degraded {
		t.Fatalf("p = %v degraded=%v, want -200/true (%s)", d.PBessVirtualKw, d.Degraded, d.Rationale)
	}
}

func TestPlanGridChargeUnclampedWithinTarget(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 500, "soc_percent": 40,
	}, QualityOK)
	d, _ := Decide(tick, arbitrageManifest(-300), testCfg())
	if d.PBessVirtualKw != -300 || d.Degraded {
		t.Fatalf("p = %v degraded=%v, want -300/false (%s)", d.PBessVirtualKw, d.Degraded, d.Rationale)
	}
}

func TestExpiredManifestFallsBackToSafePreset(t *testing.T) {
	m := arbitrageManifest(-300)
	m.ValidUntil = testTS.Add(-time.Minute)
	// The expired plan wanted a -300 kW grid charge; fallback must
	// ignore it. Serving the local deficit from the battery stays
	// allowed (that IS self-consumption), capped by rated power.
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 500, "soc_percent": 40,
	}, QualityOK)
	d, _ := Decide(tick, m, testCfg())
	if d.Preset != FallbackPreset || d.PlanSource != "fallback" {
		t.Fatalf("preset=%s source=%s, want %s/fallback", d.Preset, d.PlanSource, FallbackPreset)
	}
	if d.PBessVirtualKw < 0 {
		t.Fatalf("p = %v: fallback must never grid-charge", d.PBessVirtualKw)
	}
	if d.PBessVirtualKw != 324 {
		t.Fatalf("p = %v, want 324 (deficit discharge at rated cap)", d.PBessVirtualKw)
	}
}

func TestArbitrageWithoutIntervalDegradesToSelfConsumption(t *testing.T) {
	m := arbitrageManifest(100)
	m.Plan.Intervals[0].TS = testTS.Add(-3 * time.Hour) // no interval covers now
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 300, "load_power_kw": 100, "soc_percent": 50,
	}, QualityOK)
	d, _ := Decide(tick, m, testCfg())
	if d.ReasonCode != "no_plan_self_charge" {
		t.Fatalf("reason = %s, want no_plan_self_charge", d.ReasonCode)
	}
	if d.PBessVirtualKw != -200 {
		t.Fatalf("p = %v, want -200", d.PBessVirtualKw)
	}
}

func TestPcsShutdownBlocksDispatch(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 300, "soc_percent": 60,
		"pcs_shutdown": 1,
	}, QualityOK)
	d, _ := Decide(tick, nil, testCfg())
	if d.PBessVirtualKw != 0 || d.ReasonCode != "pcs_shutdown" {
		t.Fatalf("got p=%v reason=%s, want 0/pcs_shutdown", d.PBessVirtualKw, d.ReasonCode)
	}
}

func TestDataFaultForcesZero(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 300, "soc_percent": 60,
	}, QualityFault)
	d, _ := Decide(tick, nil, testCfg())
	if d.PBessVirtualKw != 0 || d.ReasonCode != "data_fault" {
		t.Fatalf("got p=%v reason=%s, want 0/data_fault", d.PBessVirtualKw, d.ReasonCode)
	}
}

func TestUnknownSocForcesZero(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 300,
	}, QualityOK)
	d, _ := Decide(tick, nil, testCfg())
	if d.PBessVirtualKw != 0 || !d.Degraded {
		t.Fatalf("got p=%v degraded=%v, want 0/true", d.PBessVirtualKw, d.Degraded)
	}
}

func TestManifestValidateForEdgeGates(t *testing.T) {
	m := arbitrageManifest(0)
	m.WriteEnabled = true
	if err := m.ValidateForEdge("ab"); err == nil {
		t.Fatal("write_enabled=true must be rejected in MVP build")
	}
	m = arbitrageManifest(0)
	m.Mode = Mode("auto_economic")
	if err := m.ValidateForEdge("ab"); err == nil {
		t.Fatal("auto_economic must be rejected in MVP build")
	}
	m = arbitrageManifest(0)
	if err := m.ValidateForEdge("ze"); err == nil {
		t.Fatal("foreign site_id must be rejected")
	}
	if err := arbitrageManifest(0).ValidateForEdge("ab"); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestSLAlarmBlocksDispatch(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 300, "soc_percent": 60,
		"sl_alarm_1": 0, "sl_alarm_2": 16, "sl_alarm_3": 0,
		"sl_alarm_4": 0, "sl_alarm_5": 0, "sl_alarm_6": 0,
	}, QualityOK)
	d, evs := Decide(tick, nil, testCfg())
	if d.ReasonCode != "sl_alarm" || d.PBessVirtualKw != 0 {
		t.Fatalf("got reason=%s p=%v, want sl_alarm/0", d.ReasonCode, d.PBessVirtualKw)
	}
	var alarm *Event
	for i := range evs {
		if evs[i].Code == EvSLAlarm {
			alarm = &evs[i]
		}
	}
	if alarm == nil {
		t.Fatalf("expected SL_ALARM event, got %v", evs)
	}
	if alarm.Severity != SevAlarm {
		t.Errorf("severity = %s, want alarm", alarm.Severity)
	}
	// The message must carry the hex word set so the 5-min dedup
	// re-fires when the set changes (spec §5).
	if !strings.Contains(alarm.Message, "0x0010") {
		t.Errorf("message %q does not contain hex word 0x0010", alarm.Message)
	}
}

func TestBlockingOrderDataFaultThenSLAlarmThenPcs(t *testing.T) {
	all := map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 300, "soc_percent": 60,
		"sl_alarm_1": 1, "pcs_shutdown": 1,
	}
	// data_fault wins over everything.
	d, _ := Decide(testTick(all, QualityFault), nil, testCfg())
	if d.ReasonCode != "data_fault" {
		t.Fatalf("reason = %s, want data_fault first", d.ReasonCode)
	}
	// sl_alarm wins over pcs_shutdown.
	d, _ = Decide(testTick(all, QualityOK), nil, testCfg())
	if d.ReasonCode != "sl_alarm" {
		t.Fatalf("reason = %s, want sl_alarm before pcs_shutdown", d.ReasonCode)
	}
	// All alarm words zero → pcs_shutdown.
	noAlarm := map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 300, "soc_percent": 60,
		"sl_alarm_1": 0, "sl_alarm_2": 0, "sl_alarm_3": 0,
		"sl_alarm_4": 0, "sl_alarm_5": 0, "sl_alarm_6": 0,
		"pcs_shutdown": 1,
	}
	d, _ = Decide(testTick(noAlarm, QualityOK), nil, testCfg())
	if d.ReasonCode != "pcs_shutdown" {
		t.Fatalf("reason = %s, want pcs_shutdown", d.ReasonCode)
	}
}

func TestPlanHoldWithinDeadband(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 100, "load_power_kw": 100, "soc_percent": 90,
	}, QualityOK)
	// |plan| ≤ 2 kW → plan_hold with 0, even at the SOC ceiling (spec
	// §2: SOC на стелі — наслідок, не причина).
	for _, planKw := range []float64{0, 1.5, -2} {
		d, _ := Decide(tick, arbitrageManifest(planKw), testCfg())
		if d.ReasonCode != "plan_hold" || d.PBessVirtualKw != 0 {
			t.Fatalf("plan %v: got %s %v, want plan_hold 0", planKw, d.ReasonCode, d.PBessVirtualKw)
		}
	}
	// Just above the deadband the plan acts.
	d, _ := Decide(testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 100, "soc_percent": 50,
	}, QualityOK), arbitrageManifest(2.5), testCfg())
	if d.ReasonCode != "plan_discharge" {
		t.Fatalf("plan 2.5: reason = %s, want plan_discharge", d.ReasonCode)
	}
}

func TestRecordCarriesClamps(t *testing.T) {
	// SOC at the floor clamps the discharge → clamps[] non-empty.
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 0, "load_power_kw": 300, "soc_percent": 20,
	}, QualityOK)
	d, _ := Decide(tick, nil, testCfg())
	rec := d.Record("ab")
	clamps, ok := rec["outputs"].(map[string]any)["clamps"].([]string)
	if !ok || len(clamps) == 0 {
		t.Fatalf("clamps = %v, want non-empty list", rec["outputs"].(map[string]any)["clamps"])
	}

	// Unclamped decision → empty array, never nil (spec §3.1).
	tick = testTick(map[string]float64{
		"active_pv_power_kw": 300, "load_power_kw": 100, "soc_percent": 50,
	}, QualityOK)
	d, _ = Decide(tick, nil, testCfg())
	rec = d.Record("ab")
	clamps, ok = rec["outputs"].(map[string]any)["clamps"].([]string)
	if !ok || clamps == nil || len(clamps) != 0 {
		t.Fatalf("clamps = %#v, want empty non-nil list", rec["outputs"].(map[string]any)["clamps"])
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"clamps":[]`) {
		t.Fatalf("record JSON must serialize clamps as []: %s", raw)
	}
}

func TestDecisionRecordShape(t *testing.T) {
	tick := testTick(map[string]float64{
		"active_pv_power_kw": 50, "load_power_kw": 250, "soc_percent": 67.3,
	}, QualityOK)
	d, _ := Decide(tick, nil, testCfg())
	rec := d.Record("ab")
	out, ok := rec["outputs"].(map[string]any)
	if !ok {
		t.Fatalf("outputs missing: %v", rec)
	}
	if out["would_write_40381"] != d.PBessVirtualKw {
		t.Fatalf("would_write_40381 = %v, want %v", out["would_write_40381"], d.PBessVirtualKw)
	}
	if rec["state_machine"] != "ADVISOR" || rec["site_id"] != "ab" {
		t.Fatalf("record header wrong: %v", rec)
	}
	in, ok := rec["inputs"].(map[string]any)
	if !ok || math.Abs(in["soc_percent"].(float64)-67.3) > 0.01 {
		t.Fatalf("inputs wrong: %v", rec["inputs"])
	}
}
